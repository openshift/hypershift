package awsutil

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/smithy-go"

	"github.com/go-logr/logr"
)

// LoadBalancerClients contains the clients used to delete AWS load balancer resources.
type LoadBalancerClients struct {
	ELB   awsapi.ELBAPI
	ELBV2 awsapi.ELBV2API
}

// LoadBalancerSelector limits the CLI cleanup to a VPC.
type LoadBalancerSelector struct {
	VPCID string
}

// DeleteLoadBalancers deletes load balancers and target groups selected by their VPC.
func DeleteLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, log logr.Logger) []error {
	if err := selector.validate(); err != nil {
		return []error{err}
	}

	var errs []error
	if clients.ELB != nil {
		errs = append(errs, deleteClassicLoadBalancers(ctx, clients.ELB, selector, log)...)
	}
	if clients.ELBV2 != nil {
		errs = append(errs, deleteV2LoadBalancers(ctx, clients.ELBV2, selector, log)...)
	}
	return errs
}

// DeleteLoadBalancersByName deletes only the load balancers named by hosted-cluster Services.
// The target groups attached to each ELBv2 load balancer are deleted from the same discovery result.
func DeleteLoadBalancersByName(ctx context.Context, clients LoadBalancerClients, names []string, log logr.Logger) (bool, error) {
	names = uniqueNonEmpty(names)
	if len(names) == 0 {
		return true, nil
	}

	var errs []error
	if clients.ELB != nil {
		errs = append(errs, deleteNamedClassicLoadBalancers(ctx, clients.ELB, names, log)...)
	}

	targetGroupARNs := make(map[string]struct{})
	if clients.ELBV2 != nil {
		var namedErrs []error
		targetGroupARNs, namedErrs = deleteNamedV2LoadBalancers(ctx, clients.ELBV2, names, log)
		errs = append(errs, namedErrs...)
		targetGroupErrs := deleteNamedTargetGroups(ctx, clients.ELBV2, targetGroupARNs, log)
		errs = append(errs, targetGroupErrs...)
	}

	remaining, err := countNamedLoadBalancerResources(ctx, clients, names, targetGroupARNs)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to verify named load balancer cleanup: %w", err))
	}
	return len(errs) == 0 && remaining == 0, errors.Join(errs...)
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
	if clients.ELBV2 != nil {
		currentCount, err := countV2LoadBalancerResources(ctx, clients.ELBV2, selector)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELBV2 load balancer resources: %w", err))
		}
	}
	return count, errors.Join(errs...)
}

// LoadBalancerNameFromHostname extracts an AWS load balancer name from a Service status hostname.
func LoadBalancerNameFromHostname(hostname string) string {
	firstLabel := strings.SplitN(hostname, ".", 2)[0]
	firstLabel = strings.TrimPrefix(firstLabel, "internal-")
	if firstLabel == "" {
		return ""
	}
	if lastHyphen := strings.LastIndex(firstLabel, "-"); lastHyphen > 0 {
		return firstLabel[:lastHyphen]
	}
	return firstLabel
}

func (s LoadBalancerSelector) validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("VPCID must be specified")
	}
	return nil
}

func deleteClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return append(errs, err)
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
				continue
			}
			name := aws.ToString(loadBalancer.LoadBalancerName)
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete ELB %s: %w", name, err))
			} else {
				log.Info("Deleted ELB", "name", name)
			}
		}
	}
	return errs
}

func deleteNamedClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, names []string, log logr.Logger) []error {
	var errs []error
	for _, name := range names {
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find ELB %s: %w", name, err))
			continue
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete ELB %s: %w", name, err))
			} else {
				log.Info("Deleted ELB", "name", name)
			}
		}
	}
	return errs
}

func countClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector) (int, error) {
	count := 0
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
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
	return count, nil
}

func deleteV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete ELBV2 load balancer %s: %w", aws.ToString(loadBalancer.LoadBalancerName), err))
			} else {
				log.Info("Deleted ELBV2 load balancer", "name", aws.ToString(loadBalancer.LoadBalancerName))
			}
		}
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if selector.VPCID != aws.ToString(targetGroup.VpcId) {
				continue
			}
			if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: targetGroup.TargetGroupArn}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete target group %s: %w", aws.ToString(targetGroup.TargetGroupName), err))
			} else {
				log.Info("Deleted target group", "name", aws.ToString(targetGroup.TargetGroupName))
			}
		}
	}
	return errs
}

func deleteNamedV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, names []string, log logr.Logger) (map[string]struct{}, []error) {
	targetGroupARNs := make(map[string]struct{})
	var errs []error
	for _, name := range names {
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find ELBV2 load balancer %s: %w", name, err))
			continue
		}
		for _, loadBalancer := range output.LoadBalancers {
			targetGroups, err := client.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{LoadBalancerArn: loadBalancer.LoadBalancerArn})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to find target groups for ELBV2 load balancer %s: %w", name, err))
				continue
			}
			for _, targetGroup := range targetGroups.TargetGroups {
				if targetGroup.TargetGroupArn != nil {
					targetGroupARNs[aws.ToString(targetGroup.TargetGroupArn)] = struct{}{}
				}
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete ELBV2 load balancer %s: %w", name, err))
			} else {
				log.Info("Deleted ELBV2 load balancer", "name", name)
			}
		}
	}
	return targetGroupARNs, errs
}

func deleteNamedTargetGroups(ctx context.Context, client awsapi.ELBV2API, arns map[string]struct{}, log logr.Logger) []error {
	var errs []error
	for arn := range arns {
		if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(arn)}); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete target group %s: %w", arn, err))
		} else {
			log.Info("Deleted target group", "arn", arn)
		}
	}
	return errs
}

func countV2LoadBalancerResources(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector) (int, error) {
	count := 0
	var errs []error

	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if selector.VPCID == aws.ToString(loadBalancer.VpcId) {
				count++
			}
		}
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if selector.VPCID == aws.ToString(targetGroup.VpcId) {
				count++
			}
		}
	}

	return count, errors.Join(errs...)
}

func countNamedLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, names []string, targetGroupARNs map[string]struct{}) (int, error) {
	count := 0
	var errs []error
	if clients.ELB != nil {
		for _, name := range names {
			output, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELB %s: %w", name, err))
				continue
			}
			count += len(output.LoadBalancerDescriptions)
		}
	}
	if clients.ELBV2 != nil {
		for _, name := range names {
			output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELBV2 load balancer %s: %w", name, err))
				continue
			}
			count += len(output.LoadBalancers)
		}
		for arn := range targetGroupARNs {
			output, err := clients.ELBV2.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{TargetGroupArns: []string{arn}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify target group %s: %w", arn, err))
				continue
			}
			count += len(output.TargetGroups)
		}
	}
	return count, errors.Join(errs...)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.ErrorCode()), "notfound")
}
