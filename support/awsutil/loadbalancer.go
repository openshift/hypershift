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
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"

	"github.com/go-logr/logr"
)

// LoadBalancerClients contains the clients used to delete AWS load balancer resources.
type LoadBalancerClients struct {
	ELB   awsapi.ELBAPI
	ELBV2 awsapi.ELBV2API
}

// LoadBalancerSelector limits cleanup to a VPC and, for named cleanup, a cluster tag.
type LoadBalancerSelector struct {
	VPCID   string
	InfraID string
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

// DeleteLoadBalancersByName deletes only the named load balancers owned by the hosted cluster.
// ELBv2 resources are deleted in dependency order for each load balancer.
func DeleteLoadBalancersByName(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string, log logr.Logger) (bool, error) {
	names = uniqueNonEmpty(names)
	if len(names) == 0 {
		return true, nil
	}
	if err := selector.validateNamed(); err != nil {
		return false, err
	}

	var errs []error
	if clients.ELB != nil {
		errs = append(errs, deleteNamedClassicLoadBalancers(ctx, clients.ELB, selector, names, log)...)
	}

	targetGroupARNs := make(map[string]struct{})
	if clients.ELBV2 != nil {
		var namedErrs []error
		targetGroupARNs, namedErrs = deleteNamedV2LoadBalancers(ctx, clients.ELBV2, selector, names, log)
		errs = append(errs, namedErrs...)
	}

	remaining, err := countNamedLoadBalancerResources(ctx, clients, selector, names, targetGroupARNs)
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

func (s LoadBalancerSelector) validateNamed() error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.InfraID == "" {
		return fmt.Errorf("InfraID must be specified")
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
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELB: %w", err))
			} else if err == nil {
				log.Info("Deleted ELB")
			}
		}
	}
	return errs
}

func deleteNamedClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, names []string, log logr.Logger) []error {
	var errs []error
	for _, name := range names {
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find ELB: %w", err))
			continue
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
				continue
			}
			owned, err := classicLoadBalancerHasClusterTag(ctx, client, loadBalancer.LoadBalancerName, selector.InfraID)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to inspect ELB tags: %w", err))
				continue
			}
			if !owned {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELB: %w", err))
			} else if err == nil {
				log.Info("Deleted ELB")
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
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err))
			} else if err == nil {
				log.Info("Deleted ELBV2 load balancer")
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
			if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: targetGroup.TargetGroupArn}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete target group: %w", err))
			} else if err == nil {
				log.Info("Deleted target group")
			}
		}
	}
	return errs
}

func deleteNamedV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, names []string, log logr.Logger) (map[string]struct{}, []error) {
	targetGroupARNs := make(map[string]struct{})
	var errs []error
	for _, name := range names {
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find ELBV2 load balancer: %w", err))
			continue
		}
		for _, loadBalancer := range output.LoadBalancers {
			if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
				continue
			}
			owned, err := v2ResourceHasClusterTag(ctx, client, loadBalancer.LoadBalancerArn, selector.InfraID)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to inspect ELBV2 load balancer tags: %w", err))
				continue
			}
			if !owned {
				continue
			}
			loadBalancerTargetGroups, err := deleteNamedV2LoadBalancer(ctx, client, loadBalancer, selector, log)
			for arn := range loadBalancerTargetGroups {
				targetGroupARNs[arn] = struct{}{}
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err))
			}
		}
	}
	return targetGroupARNs, errs
}

func deleteNamedV2LoadBalancer(ctx context.Context, client awsapi.ELBV2API, loadBalancer elbv2types.LoadBalancer, selector LoadBalancerSelector, log logr.Logger) (map[string]struct{}, error) {
	targetGroupARNs := make(map[string]struct{})
	targetGroupsPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{LoadBalancerArn: loadBalancer.LoadBalancerArn})
	for targetGroupsPaginator.HasMorePages() {
		targetGroups, err := targetGroupsPaginator.NextPage(ctx)
		if isNotFound(err) {
			return targetGroupARNs, nil
		}
		if err != nil {
			return targetGroupARNs, fmt.Errorf("failed to find target groups for ELBV2 load balancer: %w", err)
		}
		for _, targetGroup := range targetGroups.TargetGroups {
			if selector.VPCID != aws.ToString(targetGroup.VpcId) || targetGroup.TargetGroupArn == nil {
				continue
			}
			owned, err := v2ResourceHasClusterTag(ctx, client, targetGroup.TargetGroupArn, selector.InfraID)
			if err != nil {
				return targetGroupARNs, fmt.Errorf("failed to inspect target group tags: %w", err)
			}
			if owned {
				targetGroupARNs[aws.ToString(targetGroup.TargetGroupArn)] = struct{}{}
			}
		}
	}

	var listenerARNs []*string
	listenersPaginator := elasticloadbalancingv2.NewDescribeListenersPaginator(client, &elasticloadbalancingv2.DescribeListenersInput{LoadBalancerArn: loadBalancer.LoadBalancerArn})
	for listenersPaginator.HasMorePages() {
		listeners, err := listenersPaginator.NextPage(ctx)
		if isNotFound(err) {
			return targetGroupARNs, nil
		}
		if err != nil {
			return targetGroupARNs, fmt.Errorf("failed to find listeners for ELBV2 load balancer: %w", err)
		}
		for _, listener := range listeners.Listeners {
			listenerARNs = append(listenerARNs, listener.ListenerArn)
		}
	}
	for _, listenerARN := range listenerARNs {
		if _, err := client.DeleteListener(ctx, &elasticloadbalancingv2.DeleteListenerInput{ListenerArn: listenerARN}); err != nil && !isNotFound(err) {
			return targetGroupARNs, fmt.Errorf("failed to delete listener for ELBV2 load balancer: %w", err)
		}
	}
	for arn := range targetGroupARNs {
		if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(arn)}); err != nil && !isNotFound(err) {
			return targetGroupARNs, fmt.Errorf("failed to delete target group: %w", err)
		} else if err == nil {
			log.Info("Deleted target group")
		}
	}

	if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil && !isNotFound(err) {
		return targetGroupARNs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err)
	} else if err == nil {
		log.Info("Deleted ELBV2 load balancer")
	}
	return targetGroupARNs, nil
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

func countNamedLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string, targetGroupARNs map[string]struct{}) (int, error) {
	count := 0
	var errs []error
	if clients.ELB != nil {
		for _, name := range names {
			output, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELB: %w", err))
				continue
			}
			for _, loadBalancer := range output.LoadBalancerDescriptions {
				if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
					continue
				}
				owned, tagErr := classicLoadBalancerHasClusterTag(ctx, clients.ELB, loadBalancer.LoadBalancerName, selector.InfraID)
				if tagErr != nil {
					errs = append(errs, fmt.Errorf("failed to verify ELB tags: %w", tagErr))
					continue
				}
				if owned {
					count++
				}
			}
		}
	}
	if clients.ELBV2 != nil {
		for _, name := range names {
			output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELBV2 load balancer: %w", err))
				continue
			}
			for _, loadBalancer := range output.LoadBalancers {
				if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
					continue
				}
				owned, tagErr := v2ResourceHasClusterTag(ctx, clients.ELBV2, loadBalancer.LoadBalancerArn, selector.InfraID)
				if tagErr != nil {
					errs = append(errs, fmt.Errorf("failed to verify ELBV2 load balancer tags: %w", tagErr))
					continue
				}
				if owned {
					count++
				}
			}
		}
		for arn := range targetGroupARNs {
			output, err := clients.ELBV2.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{TargetGroupArns: []string{arn}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify target group: %w", err))
				continue
			}
			for _, targetGroup := range output.TargetGroups {
				if selector.VPCID != aws.ToString(targetGroup.VpcId) {
					continue
				}
				owned, tagErr := v2ResourceHasClusterTag(ctx, clients.ELBV2, targetGroup.TargetGroupArn, selector.InfraID)
				if tagErr != nil {
					errs = append(errs, fmt.Errorf("failed to verify target group tags: %w", tagErr))
					continue
				}
				if owned {
					count++
				}
			}
		}
	}
	return count, errors.Join(errs...)
}

func classicLoadBalancerHasClusterTag(ctx context.Context, client awsapi.ELBAPI, name *string, infraID string) (bool, error) {
	output, err := client.DescribeTags(ctx, &elasticloadbalancing.DescribeTagsInput{LoadBalancerNames: []string{aws.ToString(name)}})
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tagKey := fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
	for _, description := range output.TagDescriptions {
		if aws.ToString(description.LoadBalancerName) != aws.ToString(name) {
			continue
		}
		for _, tag := range description.Tags {
			if aws.ToString(tag.Key) == tagKey {
				return true, nil
			}
		}
	}
	return false, nil
}

func v2ResourceHasClusterTag(ctx context.Context, client awsapi.ELBV2API, arn *string, infraID string) (bool, error) {
	output, err := client.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{aws.ToString(arn)}})
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tagKey := fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
	for _, description := range output.TagDescriptions {
		if aws.ToString(description.ResourceArn) != aws.ToString(arn) {
			continue
		}
		for _, tag := range description.Tags {
			if aws.ToString(tag.Key) == tagKey {
				return true, nil
			}
		}
	}
	return false, nil
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
