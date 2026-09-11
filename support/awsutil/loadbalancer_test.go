package awsutil

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	resourcegroupstaggingapitypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

type fakeResourceTaggingClient struct {
	mappings map[string][]resourcegroupstaggingapitypes.ResourceTagMapping
	err      error
}

func (f *fakeResourceTaggingClient) GetResources(_ context.Context, input *resourcegroupstaggingapi.GetResourcesInput, _ ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &resourcegroupstaggingapi.GetResourcesOutput{
		ResourceTagMappingList: f.mappings[input.ResourceTypeFilters[0]],
	}, nil
}

func TestDeleteLoadBalancers(t *testing.T) {
	t.Run("When cleanup is tag scoped it should delete only resources owned by the hosted cluster", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbClient := awsapi.NewMockELBAPI(ctrl)
		elbv2Client := awsapi.NewMockELBV2API(ctrl)

		const (
			vpcID                  = "vpc-owned"
			classicLoadBalancer    = "classic-owned"
			classicLoadBalancerARN = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/classic-owned"
			v2LoadBalancerARN      = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/v2-owned/123"
			targetGroupARN         = "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/owned/123"
		)

		taggingClient := &fakeResourceTaggingClient{mappings: map[string][]resourcegroupstaggingapitypes.ResourceTagMapping{
			"elasticloadbalancing:loadbalancer": {
				{ResourceARN: aws.String(classicLoadBalancerARN)},
				{ResourceARN: aws.String(v2LoadBalancerARN)},
			},
			"elasticloadbalancing:targetgroup": {
				{ResourceARN: aws.String(targetGroupARN)},
			},
		}}

		elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&elasticloadbalancing.DescribeLoadBalancersOutput{
				LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
					{LoadBalancerName: aws.String(classicLoadBalancer), VPCId: aws.String(vpcID)},
					{LoadBalancerName: aws.String("classic-other"), VPCId: aws.String(vpcID)},
				},
			}, nil,
		)
		elbClient.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
			LoadBalancerName: aws.String(classicLoadBalancer),
		}, gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)

		elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&elasticloadbalancingv2.DescribeLoadBalancersOutput{
				LoadBalancers: []elbv2types.LoadBalancer{
					{LoadBalancerArn: aws.String(v2LoadBalancerARN), LoadBalancerName: aws.String("v2-owned"), VpcId: aws.String(vpcID)},
					{LoadBalancerArn: aws.String("arn:other"), LoadBalancerName: aws.String("v2-other"), VpcId: aws.String(vpcID)},
				},
			}, nil,
		)
		elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
			LoadBalancerArn: aws.String(v2LoadBalancerARN),
		}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
		elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&elasticloadbalancingv2.DescribeTargetGroupsOutput{
				TargetGroups: []elbv2types.TargetGroup{
					{TargetGroupArn: aws.String(targetGroupARN), TargetGroupName: aws.String("owned"), VpcId: aws.String(vpcID)},
					{TargetGroupArn: aws.String("arn:other-target-group"), TargetGroupName: aws.String("other"), VpcId: aws.String(vpcID)},
				},
			}, nil,
		)
		elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
			TargetGroupArn: aws.String(targetGroupARN),
		}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)

		errs := DeleteLoadBalancers(context.Background(), LoadBalancerClients{
			ELB:     elbClient,
			ELBV2:   elbv2Client,
			Tagging: taggingClient,
		}, LoadBalancerSelector{
			VPCID:    vpcID,
			TagKey:   KubernetesClusterTagKey("cluster-id"),
			TagValue: KubernetesClusterTagValueOwned,
		}, logr.Discard())
		if len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
	})

	t.Run("When tag discovery fails it should not attempt deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbClient := awsapi.NewMockELBAPI(ctrl)
		elbv2Client := awsapi.NewMockELBV2API(ctrl)
		elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		client := &fakeResourceTaggingClient{err: errors.New("tagging unavailable")}
		errs := DeleteLoadBalancers(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client, Tagging: client}, LoadBalancerSelector{
			TagKey:   KubernetesClusterTagKey("cluster-id"),
			TagValue: KubernetesClusterTagValueOwned,
		}, logr.Discard())
		if len(errs) != 1 {
			t.Fatalf("expected one error, got %v", errs)
		}
	})
}

func TestCountLoadBalancerResources(t *testing.T) {
	t.Run("When selected resources remain it should count load balancers and target groups", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbClient := awsapi.NewMockELBAPI(ctrl)
		elbv2Client := awsapi.NewMockELBV2API(ctrl)
		const (
			vpcID                  = "vpc-owned"
			classicLoadBalancer    = "classic-owned"
			classicLoadBalancerARN = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/classic-owned"
			v2LoadBalancerARN      = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/v2-owned/123"
			targetGroupARN         = "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/owned/123"
		)
		taggingClient := &fakeResourceTaggingClient{mappings: map[string][]resourcegroupstaggingapitypes.ResourceTagMapping{
			"elasticloadbalancing:loadbalancer": {{ResourceARN: aws.String(classicLoadBalancerARN)}, {ResourceARN: aws.String(v2LoadBalancerARN)}},
			"elasticloadbalancing:targetgroup":  {{ResourceARN: aws.String(targetGroupARN)}},
		}}
		elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
			LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
				{LoadBalancerName: aws.String(classicLoadBalancer), VPCId: aws.String(vpcID)},
				{LoadBalancerName: aws.String("classic-other"), VPCId: aws.String(vpcID)},
			},
		}, nil)
		elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbv2types.LoadBalancer{
				{LoadBalancerArn: aws.String(v2LoadBalancerARN), VpcId: aws.String(vpcID)},
				{LoadBalancerArn: aws.String("arn:other"), VpcId: aws.String(vpcID)},
			},
		}, nil)
		elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
			TargetGroups: []elbv2types.TargetGroup{
				{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)},
				{TargetGroupArn: aws.String("arn:other-target-group"), VpcId: aws.String(vpcID)},
			},
		}, nil)

		count, err := CountLoadBalancerResources(context.Background(), LoadBalancerClients{
			ELB: elbClient, ELBV2: elbv2Client, Tagging: taggingClient,
		}, LoadBalancerSelector{
			VPCID: vpcID, TagKey: KubernetesClusterTagKey("cluster-id"), TagValue: KubernetesClusterTagValueOwned,
		})
		if err != nil {
			t.Fatalf("expected no errors, got %v", err)
		}
		if count != 3 {
			t.Fatalf("expected three selected resources, got %d", count)
		}
	})

	t.Run("When listing selected resources fails it should return the error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbClient := awsapi.NewMockELBAPI(ctrl)
		elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("describe unavailable"))

		count, err := CountLoadBalancerResources(context.Background(), LoadBalancerClients{ELB: elbClient}, LoadBalancerSelector{VPCID: "vpc-owned"})
		if count != 0 {
			t.Fatalf("expected no selected resources, got %d", count)
		}
		if err == nil || !strings.Contains(err.Error(), "failed to count ELB load balancers") {
			t.Fatalf("expected listing error, got %v", err)
		}
	})
}

func TestResourceNameInARNs(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		arns         map[string]struct{}
		want         bool
	}{
		{
			name:         "When a classic load balancer ARN matches, it should return true",
			resourceName: "classic-owned",
			arns:         map[string]struct{}{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/classic-owned": {}},
			want:         true,
		},
		{
			name:         "When a non-classic load balancer ARN ends with the name, it should return false",
			resourceName: "classic-owned",
			arns:         map[string]struct{}{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/classic-owned": {}},
		},
		{
			name:         "When the name is empty, it should return false",
			resourceName: "",
			arns:         map[string]struct{}{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/": {}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resourceNameInARNs(test.resourceName, test.arns); got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}
