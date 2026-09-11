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
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	resourcegroupstaggingapitypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	"github.com/go-logr/logr"
)

const (
	kubernetesClusterTagPrefix     = "kubernetes.io/cluster/"
	KubernetesClusterTagValueOwned = "owned"
	CloudControllerTokenMountPath  = "/var/run/secrets/openshift/serviceaccount/cloud-controller"
	IngressTokenMountPath          = "/var/run/secrets/openshift/serviceaccount/ingress"
)

// KubernetesClusterTagKey returns the tag used by AWS cloud controllers to identify cluster-owned resources.
func KubernetesClusterTagKey(infraID string) string {
	return kubernetesClusterTagPrefix + infraID
}

// ResourceTaggingAPI contains the tagging API used to select resources before deleting them.
type ResourceTaggingAPI interface {
	GetResources(context.Context, *resourcegroupstaggingapi.GetResourcesInput, ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error)
}

// LoadBalancerClients contains clients whose permissions are intentionally supplied by different guest roles.
type LoadBalancerClients struct {
	ELB     awsapi.ELBAPI
	ELBV2   awsapi.ELBV2API
	Tagging ResourceTaggingAPI
}

// LoadBalancerSelector describes either the VPC-wide CLI cleanup or the tag-scoped HCCO cleanup.
type LoadBalancerSelector struct {
	VPCID    string
	TagKey   string
	TagValue string
}

// DeleteLoadBalancers deletes only the resources selected by selector. Tag-scoped selection is required for
// cleanup from a shared VPC; it never falls back to deleting every load balancer in that VPC.
func DeleteLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, log logr.Logger) []error {
	if err := selector.validate(); err != nil {
		return []error{err}
	}

	var taggedLoadBalancers, taggedTargetGroups map[string]struct{}
	var errs []error
	if selector.TagKey != "" {
		if clients.Tagging == nil {
			return []error{fmt.Errorf("resource tagging client is required for tag-scoped load balancer cleanup")}
		}
		var err error
		taggedLoadBalancers, err = taggedResourceARNs(ctx, clients.Tagging, "elasticloadbalancing:loadbalancer", selector)
		if err != nil {
			return []error{fmt.Errorf("failed to find tagged load balancers: %w", err)}
		}
		taggedTargetGroups, err = taggedResourceARNs(ctx, clients.Tagging, "elasticloadbalancing:targetgroup", selector)
		if err != nil {
			return []error{fmt.Errorf("failed to find tagged target groups: %w", err)}
		}
	}

	if clients.ELB != nil {
		errs = append(errs, deleteClassicLoadBalancers(ctx, clients.ELB, selector, taggedLoadBalancers, log)...)
	}
	if clients.ELBV2 != nil {
		errs = append(errs, deleteV2LoadBalancers(ctx, clients.ELBV2, selector, taggedLoadBalancers, taggedTargetGroups, log)...)
	}
	return errs
}

// CountLoadBalancerResources counts selected load balancers and target groups that are still present.
func CountLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector) (int, error) {
	if err := selector.validate(); err != nil {
		return 0, err
	}

	var taggedLoadBalancers, taggedTargetGroups map[string]struct{}
	var errs []error
	if selector.TagKey != "" {
		if clients.Tagging == nil {
			return 0, fmt.Errorf("resource tagging client is required for tag-scoped load balancer cleanup")
		}
		var err error
		taggedLoadBalancers, err = taggedResourceARNs(ctx, clients.Tagging, "elasticloadbalancing:loadbalancer", selector)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find tagged load balancers: %w", err))
		}
		taggedTargetGroups, err = taggedResourceARNs(ctx, clients.Tagging, "elasticloadbalancing:targetgroup", selector)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find tagged target groups: %w", err))
		}
	}

	count := 0
	if clients.ELB != nil {
		currentCount, err := countClassicLoadBalancers(ctx, clients.ELB, selector, taggedLoadBalancers)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELB load balancers: %w", err))
		}
	}
	if clients.ELBV2 != nil {
		currentCount, err := countV2LoadBalancerResources(ctx, clients.ELBV2, selector, taggedLoadBalancers, taggedTargetGroups)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELBV2 load balancer resources: %w", err))
		}
	}
	return count, errors.Join(errs...)
}

func (s LoadBalancerSelector) validate() error {
	if s.VPCID == "" && s.TagKey == "" {
		return fmt.Errorf("either VPCID or TagKey must be specified")
	}
	if s.TagKey != "" && s.TagValue == "" {
		return fmt.Errorf("TagValue must be specified with TagKey")
	}
	return nil
}

func taggedResourceARNs(ctx context.Context, client ResourceTaggingAPI, resourceType string, selector LoadBalancerSelector) (map[string]struct{}, error) {
	arns := make(map[string]struct{})
	var paginationToken *string
	for {
		output, err := client.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
			PaginationToken:     paginationToken,
			ResourceTypeFilters: []string{resourceType},
			TagFilters:          []resourcegroupstaggingapitypes.TagFilter{{Key: aws.String(selector.TagKey), Values: []string{selector.TagValue}}},
		})
		if err != nil {
			return nil, err
		}
		for _, mapping := range output.ResourceTagMappingList {
			if mapping.ResourceARN != nil {
				arns[aws.ToString(mapping.ResourceARN)] = struct{}{}
			}
		}
		if output.PaginationToken == nil || aws.ToString(output.PaginationToken) == "" {
			return arns, nil
		}
		paginationToken = output.PaginationToken
	}
}

func deleteClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, taggedARNs map[string]struct{}, log logr.Logger) []error {
	var errs []error
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return append(errs, err)
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			name := aws.ToString(loadBalancer.LoadBalancerName)
			if !selectedLoadBalancer(selector, aws.ToString(loadBalancer.VPCId), name, taggedARNs) {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete ELB %s: %w", name, err))
			} else {
				log.Info("Deleted ELB", "name", name)
			}
		}
	}
	return errs
}

func countClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, taggedARNs map[string]struct{}) (int, error) {
	count := 0
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return count, err
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selectedLoadBalancer(selector, aws.ToString(loadBalancer.VPCId), aws.ToString(loadBalancer.LoadBalancerName), taggedARNs) {
				count++
			}
		}
	}
	return count, nil
}

func deleteV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, taggedLoadBalancerARNs, taggedTargetGroupARNs map[string]struct{}, log logr.Logger) []error {
	var errs []error
	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			arn := aws.ToString(loadBalancer.LoadBalancerArn)
			if !selectedLoadBalancer(selector, aws.ToString(loadBalancer.VpcId), arn, taggedLoadBalancerARNs) {
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
			arn := aws.ToString(targetGroup.TargetGroupArn)
			if !selectedTargetGroup(selector, aws.ToString(targetGroup.VpcId), arn, taggedTargetGroupARNs) {
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

func countV2LoadBalancerResources(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, taggedLoadBalancerARNs, taggedTargetGroupARNs map[string]struct{}) (int, error) {
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
			if selectedLoadBalancer(selector, aws.ToString(loadBalancer.VpcId), aws.ToString(loadBalancer.LoadBalancerArn), taggedLoadBalancerARNs) {
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
			if selectedTargetGroup(selector, aws.ToString(targetGroup.VpcId), aws.ToString(targetGroup.TargetGroupArn), taggedTargetGroupARNs) {
				count++
			}
		}
	}

	return count, errors.Join(errs...)
}

func selectedLoadBalancer(selector LoadBalancerSelector, vpcID, identifier string, taggedARNs map[string]struct{}) bool {
	if selector.VPCID != "" && selector.VPCID != vpcID {
		return false
	}
	if selector.TagKey == "" {
		return true
	}
	if _, ok := taggedARNs[identifier]; ok {
		return true
	}
	return resourceNameInARNs(identifier, taggedARNs)
}

func selectedTargetGroup(selector LoadBalancerSelector, vpcID, arn string, taggedARNs map[string]struct{}) bool {
	if selector.VPCID != "" && selector.VPCID != vpcID {
		return false
	}
	if selector.TagKey == "" {
		return true
	}
	_, ok := taggedARNs[arn]
	return ok
}

func resourceNameInARNs(name string, arns map[string]struct{}) bool {
	if name == "" {
		return false
	}
	for resourceARN := range arns {
		// Classic load balancer ARNs end with :loadbalancer/<name>.
		if strings.HasSuffix(resourceARN, ":loadbalancer/"+name) {
			return true
		}
	}
	return false
}
