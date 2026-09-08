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

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

func TestDeleteLoadBalancers(t *testing.T) {
	t.Run("When resources are in the selected VPC, it should delete those resources", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbClient := awsapi.NewMockELBAPI(ctrl)
		elbv2Client := awsapi.NewMockELBV2API(ctrl)

		const vpcID = "vpc-owned"
		elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&elasticloadbalancing.DescribeLoadBalancersOutput{
				LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
					{LoadBalancerName: aws.String("classic-owned"), VPCId: aws.String(vpcID)},
					{LoadBalancerName: aws.String("classic-other"), VPCId: aws.String("vpc-other")},
				},
			}, nil,
		)
		elbClient.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
			LoadBalancerName: aws.String("classic-owned"),
		}, gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)

		elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&elasticloadbalancingv2.DescribeLoadBalancersOutput{
				LoadBalancers: []elbv2types.LoadBalancer{
					{LoadBalancerArn: aws.String("arn:owned"), LoadBalancerName: aws.String("v2-owned"), VpcId: aws.String(vpcID)},
					{LoadBalancerArn: aws.String("arn:other"), LoadBalancerName: aws.String("v2-other"), VpcId: aws.String("vpc-other")},
				},
			}, nil,
		)
		elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
			LoadBalancerArn: aws.String("arn:owned"),
		}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
		elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&elasticloadbalancingv2.DescribeTargetGroupsOutput{
				TargetGroups: []elbv2types.TargetGroup{
					{TargetGroupArn: aws.String("arn:target-owned"), TargetGroupName: aws.String("target-owned"), VpcId: aws.String(vpcID)},
					{TargetGroupArn: aws.String("arn:target-other"), TargetGroupName: aws.String("target-other"), VpcId: aws.String("vpc-other")},
				},
			}, nil,
		)
		elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
			TargetGroupArn: aws.String("arn:target-owned"),
		}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)

		errs := DeleteLoadBalancers(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client}, LoadBalancerSelector{VPCID: vpcID}, logr.Discard())
		if len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
	})

	t.Run("When the VPC is missing, it should return a validation error", func(t *testing.T) {
		errs := DeleteLoadBalancers(context.Background(), LoadBalancerClients{}, LoadBalancerSelector{}, logr.Discard())
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "VPCID must be specified") {
			t.Fatalf("expected VPC validation error, got %v", errs)
		}
	})
}

func TestDeleteLoadBalancersByName(t *testing.T) {
	ctrl := gomock.NewController(t)
	elbClient := awsapi.NewMockELBAPI(ctrl)
	elbv2Client := awsapi.NewMockELBV2API(ctrl)

	const (
		name           = "cluster-lb"
		v2ARN          = "arn:v2"
		targetGroupARN = "arn:target-group"
	)
	elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancing.DescribeLoadBalancersInput{
		LoadBalancerNames: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
		LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: aws.String(name)}},
	}, nil)
	elbClient.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
		LoadBalancerName: aws.String(name),
	}, gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)
	elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancing.DescribeLoadBalancersInput{
		LoadBalancerNames: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{}, nil)

	elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(v2ARN), LoadBalancerName: aws.String(name)}},
	}, nil)
	elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(v2ARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN)}},
	}, nil)
	elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(v2ARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
	elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil)
	elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client}, []string{name, name}, logr.Discard())
	if err != nil {
		t.Fatalf("expected no errors, got %v", err)
	}
	if !removed {
		t.Fatal("expected named load balancers to be removed")
	}
}

func TestCountLoadBalancerResources(t *testing.T) {
	t.Run("When selected resources remain, it should count load balancers and target groups", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbClient := awsapi.NewMockELBAPI(ctrl)
		elbv2Client := awsapi.NewMockELBV2API(ctrl)
		const vpcID = "vpc-owned"

		elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
			LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
				{LoadBalancerName: aws.String("classic-owned"), VPCId: aws.String(vpcID)},
				{LoadBalancerName: aws.String("classic-other"), VPCId: aws.String("vpc-other")},
			},
		}, nil)
		elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbv2types.LoadBalancer{
				{LoadBalancerArn: aws.String("arn:owned"), VpcId: aws.String(vpcID)},
				{LoadBalancerArn: aws.String("arn:other"), VpcId: aws.String("vpc-other")},
			},
		}, nil)
		elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
			TargetGroups: []elbv2types.TargetGroup{
				{TargetGroupArn: aws.String("arn:target-owned"), VpcId: aws.String(vpcID)},
				{TargetGroupArn: aws.String("arn:target-other"), VpcId: aws.String("vpc-other")},
			},
		}, nil)

		count, err := CountLoadBalancerResources(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client}, LoadBalancerSelector{VPCID: vpcID})
		if err != nil {
			t.Fatalf("expected no errors, got %v", err)
		}
		if count != 3 {
			t.Fatalf("expected three selected resources, got %d", count)
		}
	})

	t.Run("When listing selected resources fails, it should return the error", func(t *testing.T) {
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

func TestLoadBalancerNameFromHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{
			name:     "When the hostname is internal, it should remove the internal prefix and generated suffix",
			hostname: "internal-cluster-lb-123456.us-east-1.elb.amazonaws.com",
			want:     "cluster-lb",
		},
		{
			name:     "When the hostname has no generated suffix, it should return the first label",
			hostname: "cluster.example.com",
			want:     "cluster",
		},
		{
			name: "When the hostname is empty, it should return an empty name",
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := LoadBalancerNameFromHostname(test.hostname); got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}
