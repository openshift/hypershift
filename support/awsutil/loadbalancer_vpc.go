package awsutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/go-logr/logr"
)

// DeleteLoadBalancers deletes load balancers and target groups selected by their VPC.
// It does not check cluster ownership tags and is intended for full infrastructure
// teardown. Shared-VPC cleanup must use the named or recorded cleanup helpers.
func DeleteLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, log logr.Logger) []error {
	if err := selector.validate(); err != nil {
		return []error{err}
	}

	var errs []error
	if clients.ELB != nil {
		errs = append(errs, deleteClassicLoadBalancers(ctx, clients.ELB, selector, log)...)
	}
	if appendContextError(ctx, &errs) {
		return errs
	}
	if clients.ELBV2 != nil {
		errs = append(errs, deleteV2LoadBalancers(ctx, clients.ELBV2, selector, log)...)
	}
	return errs
}

// CountLoadBalancerResources counts load balancers and target groups that remain in a VPC.
func CountLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector) (int, error) {
	if err := selector.validate(); err != nil {
		return 0, err
	}

	count := 0
	var errs []error
	if clients.ELB != nil {
		currentCount, err := countClassicLoadBalancers(ctx, clients.ELB, selector)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELB load balancers: %w", err))
		}
	}
	if appendContextError(ctx, &errs) {
		return count, errors.Join(errs...)
	}
	if clients.ELBV2 != nil {
		currentCount, err := countV2LoadBalancerResources(ctx, clients.ELBV2, selector)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELBV2 load balancer resources: %w", err))
		}
	}
	return count, errors.Join(errs...)
}

func deleteClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return errs
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return append(errs, err)
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if appendContextError(ctx, &errs) {
				return errs
			}
			if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELB: %w", err))
			} else if err == nil {
				log.Info("Deleted ELB")
			}
		}
	}
	appendContextError(ctx, &errs)
	return errs
}

func countClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector) (int, error) {
	count := 0
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return count, err
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selector.VPCID == aws.ToString(loadBalancer.VPCId) {
				count++
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return count, err
	}
	return count, nil
}

func deleteV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return errs
		}
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if appendContextError(ctx, &errs) {
				return errs
			}
			if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err))
			} else if err == nil {
				log.Info("Deleted ELBV2 load balancer")
			}
		}
	}
	if appendContextError(ctx, &errs) {
		return errs
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return errs
		}
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if appendContextError(ctx, &errs) {
				return errs
			}
			if selector.VPCID != aws.ToString(targetGroup.VpcId) {
				continue
			}
			if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: targetGroup.TargetGroupArn}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete target group: %w", err))
			} else if err == nil {
				log.Info("Deleted target group")
			}
		}
	}
	appendContextError(ctx, &errs)
	return errs
}

func countV2LoadBalancerResources(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector) (int, error) {
	count := 0
	var errs []error

	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			break
		}
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if appendContextError(ctx, &errs) {
				break
			}
			if selector.VPCID == aws.ToString(loadBalancer.VpcId) {
				count++
			}
		}
	}
	if appendContextError(ctx, &errs) {
		return count, errors.Join(errs...)
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			break
		}
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if appendContextError(ctx, &errs) {
				break
			}
			if selector.VPCID == aws.ToString(targetGroup.VpcId) {
				count++
			}
		}
	}
	appendContextError(ctx, &errs)

	return count, errors.Join(errs...)
}
