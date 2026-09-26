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
	"github.com/aws/smithy-go"

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
		vpcID          = "vpc-owned"
		infraID        = "infra-id"
		v2ARN          = "arn:v2"
		listenerARN    = "arn:listener"
		targetGroupARN = "arn:target-group"
	)
	selector := LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}
	classicDescribe := elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancing.DescribeLoadBalancersInput{
		LoadBalancerNames: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
		LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: aws.String(name), VPCId: aws.String(vpcID)}},
	}, nil)
	classicTags := elbClient.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancing.DescribeTagsInput{
		LoadBalancerNames: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancing.DescribeTagsOutput{
		TagDescriptions: []elbtypes.TagDescription{{
			LoadBalancerName: aws.String(name),
			Tags:             []elbtypes.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	classicDelete := elbClient.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
		LoadBalancerName: aws.String(name),
	}, gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)
	classicVerify := elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancing.DescribeLoadBalancersInput{
		LoadBalancerNames: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{}, nil)

	v2Describe := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(v2ARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
	}, nil)
	v2Tags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{v2ARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(v2ARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2TargetGroups := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(v2ARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2TargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2Listeners := elbv2Client.EXPECT().DescribeListeners(gomock.Any(), &elasticloadbalancingv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(v2ARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{
		Listeners: []elbv2types.Listener{{ListenerArn: aws.String(listenerARN)}},
	}, nil)
	v2DeleteListener := elbv2Client.EXPECT().DeleteListener(gomock.Any(), &elasticloadbalancingv2.DeleteListenerInput{
		ListenerArn: aws.String(listenerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteListenerOutput{}, nil)
	v2DeleteTargetGroup := elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	v2DeleteLoadBalancer := elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(v2ARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
	v2VerifyLoadBalancer := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil)
	v2VerifyTargetGroup := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)
	gomock.InOrder(classicDescribe, classicTags, classicDelete, v2Describe, v2Tags, v2TargetGroups, v2TargetGroupTags, v2Listeners, v2DeleteListener, v2DeleteTargetGroup, v2DeleteLoadBalancer, classicVerify, v2VerifyLoadBalancer, v2VerifyTargetGroup)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client}, selector, []string{name, name}, logr.Discard())
	if err != nil {
		t.Fatalf("expected no errors, got %v", err)
	}
	if !removed {
		t.Fatal("expected named load balancers to be removed")
	}

	t.Run("When a named load balancer is outside the VPC or cluster scope, it should not delete it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		elbv2Client := awsapi.NewMockELBV2API(ctrl)
		const (
			name       = "cluster-lb"
			vpcID      = "vpc-owned"
			infraID    = "infra-id"
			ownedARN   = "arn:unowned"
			outsideARN = "arn:outside"
		)
		selector := LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}
		loadBalancers := []elbv2types.LoadBalancer{
			{LoadBalancerArn: aws.String(ownedARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)},
			{LoadBalancerArn: aws.String(outsideARN), LoadBalancerName: aws.String(name), VpcId: aws.String("vpc-other")},
		}
		initialDescribe := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
			LoadBalancers: loadBalancers,
		}, nil)
		unownedTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
			TagDescriptions: []elbv2types.TagDescription{{
				ResourceArn: aws.String(ownedARN),
				Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/other-infra")}},
			}},
		}, nil)
		verifyDescribe := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
			LoadBalancers: loadBalancers,
		}, nil)
		verifyUnownedTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
			TagDescriptions: []elbv2types.TagDescription{{
				ResourceArn: aws.String(ownedARN),
				Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/other-infra")}},
			}},
		}, nil)
		gomock.InOrder(initialDescribe, unownedTags, verifyDescribe, verifyUnownedTags)

		removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELBV2: elbv2Client}, selector, []string{name}, logr.Discard())
		if err != nil {
			t.Fatalf("expected no errors, got %v", err)
		}
		if !removed {
			t.Fatal("expected out-of-scope resources to be ignored")
		}
	})
}

func TestDeleteNamedV2LoadBalancerPaginatesDiscovery(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBV2API(ctrl)

	const (
		vpcID             = "vpc-owned"
		infraID           = "infra-id"
		loadBalancerARN   = "arn:load-balancer"
		targetGroupARNOne = "arn:target-group-one"
		targetGroupARNTwo = "arn:target-group-two"
		listenerARNOne    = "arn:listener-one"
		listenerARNTwo    = "arn:listener-two"
	)
	selector := LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}

	targetGroupsPageOne := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARNOne), VpcId: aws.String(vpcID)}},
		NextMarker:   aws.String("target-groups-page-two"),
	}, nil)
	targetGroupOneTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARNOne},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARNOne),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	targetGroupsPageTwo := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
		Marker:          aws.String("target-groups-page-two"),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARNTwo), VpcId: aws.String(vpcID)}},
	}, nil)
	targetGroupTwoTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARNTwo},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARNTwo),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	listenersPageOne := client.EXPECT().DescribeListeners(gomock.Any(), &elasticloadbalancingv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{
		Listeners:  []elbv2types.Listener{{ListenerArn: aws.String(listenerARNOne)}},
		NextMarker: aws.String("listeners-page-two"),
	}, nil)
	listenersPageTwo := client.EXPECT().DescribeListeners(gomock.Any(), &elasticloadbalancingv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
		Marker:          aws.String("listeners-page-two"),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{
		Listeners: []elbv2types.Listener{{ListenerArn: aws.String(listenerARNTwo)}},
	}, nil)
	deleteListenerOne := client.EXPECT().DeleteListener(gomock.Any(), &elasticloadbalancingv2.DeleteListenerInput{
		ListenerArn: aws.String(listenerARNOne),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteListenerOutput{}, nil)
	deleteListenerTwo := client.EXPECT().DeleteListener(gomock.Any(), &elasticloadbalancingv2.DeleteListenerInput{
		ListenerArn: aws.String(listenerARNTwo),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteListenerOutput{}, nil)
	deleteTargetGroupOne := client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARNOne),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	deleteTargetGroupTwo := client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARNTwo),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	deleteLoadBalancer := client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
	gomock.InOrder(targetGroupsPageOne, targetGroupOneTags, targetGroupsPageTwo, targetGroupTwoTags, listenersPageOne, listenersPageTwo, deleteListenerOne, deleteListenerTwo, deleteTargetGroupOne, deleteTargetGroupTwo, deleteLoadBalancer)

	targetGroupARNs, err := deleteNamedV2LoadBalancer(context.Background(), client, elbv2types.LoadBalancer{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, selector, logr.Discard())
	if err != nil {
		t.Fatalf("expected no errors, got %v", err)
	}
	if len(targetGroupARNs) != 2 {
		t.Fatalf("expected two target groups, got %d", len(targetGroupARNs))
	}
}

func TestDeleteLoadBalancersByNameTreatsNotFoundAsSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	elbClient := awsapi.NewMockELBAPI(ctrl)
	elbv2Client := awsapi.NewMockELBV2API(ctrl)

	const (
		name           = "cluster-lb"
		vpcID          = "vpc-owned"
		infraID        = "infra-id"
		v2ARN          = "arn:v2"
		targetGroupARN = "arn:target-group"
	)
	selector := LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}
	notFound := &smithy.GenericAPIError{Code: "LoadBalancerNotFound", Message: "not found"}
	targetGroupNotFound := &smithy.GenericAPIError{Code: "TargetGroupNotFound", Message: "not found"}

	classicDescribe := elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
		LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: aws.String(name), VPCId: aws.String(vpcID)}},
	}, nil)
	classicTags := elbClient.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeTagsOutput{
		TagDescriptions: []elbtypes.TagDescription{{
			LoadBalancerName: aws.String(name),
			Tags:             []elbtypes.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	classicDelete := elbClient.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)
	classicVerify := elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)

	v2Describe := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(v2ARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
	}, nil)
	v2Tags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(v2ARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2TargetGroups := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2TargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2Listeners := elbv2Client.EXPECT().DescribeListeners(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{}, nil)
	v2DeleteTargetGroup := elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, targetGroupNotFound)
	v2DeleteLoadBalancer := elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)
	v2VerifyLoadBalancer := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)
	// The initial target-group lookup returns the target group; the verification lookup observes its deletion.
	v2VerifyTargetGroup := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, targetGroupNotFound)
	gomock.InOrder(classicDescribe, classicTags, classicDelete, v2Describe, v2Tags, v2TargetGroups, v2TargetGroupTags, v2Listeners, v2DeleteTargetGroup, v2DeleteLoadBalancer, classicVerify, v2VerifyLoadBalancer, v2VerifyTargetGroup)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client}, selector, []string{name}, logr.Discard())
	if err != nil {
		t.Fatalf("expected not-found deletes to be idempotent, got %v", err)
	}
	if !removed {
		t.Fatal("expected named load balancer resources to be removed")
	}
}

func TestDeleteLoadBalancersByNameRetainsLoadBalancerWhenTargetGroupDeletionFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	elbv2Client := awsapi.NewMockELBV2API(ctrl)

	const (
		name           = "cluster-lb"
		vpcID          = "vpc-owned"
		infraID        = "infra-id"
		v2ARN          = "arn:v2"
		listenerARN    = "arn:listener"
		targetGroupARN = "arn:target-group"
	)
	selector := LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}
	v2Describe := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(v2ARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
	}, nil)
	v2Tags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(v2ARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2TargetGroups := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2TargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2Listeners := elbv2Client.EXPECT().DescribeListeners(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{
		Listeners: []elbv2types.Listener{{ListenerArn: aws.String(listenerARN)}},
	}, nil)
	v2DeleteListener := elbv2Client.EXPECT().DeleteListener(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DeleteListenerOutput{}, nil)
	v2DeleteTargetGroup := elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("target group is still in use"))
	elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	v2VerifyLoadBalancer := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(v2ARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
	}, nil)
	v2VerifyLoadBalancerTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(v2ARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	v2VerifyTargetGroup := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2VerifyTargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID)}},
		}},
	}, nil)
	gomock.InOrder(v2Describe, v2Tags, v2TargetGroups, v2TargetGroupTags, v2Listeners, v2DeleteListener, v2DeleteTargetGroup, v2VerifyLoadBalancer, v2VerifyLoadBalancerTags, v2VerifyTargetGroup, v2VerifyTargetGroupTags)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELBV2: elbv2Client}, selector, []string{name}, logr.Discard())
	if removed {
		t.Fatal("expected cleanup to remain incomplete")
	}
	if err == nil || !strings.Contains(err.Error(), "target group is still in use") {
		t.Fatalf("expected target group deletion error, got %v", err)
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
