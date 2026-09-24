package awsutil

import (
	"context"
	"errors"
	"fmt"
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
			Tags:             []elbtypes.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
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
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
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
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
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
	v2OrphanTargetGroupScan := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)
	v2VerifyLoadBalancer := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil)
	v2VerifyTargetGroup := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)
	gomock.InOrder(classicDescribe, classicTags, classicDelete, v2Describe, v2Tags, v2TargetGroups, v2TargetGroupTags, v2Listeners, v2DeleteListener, v2DeleteTargetGroup, v2DeleteLoadBalancer, v2OrphanTargetGroupScan, classicVerify, v2VerifyLoadBalancer, v2VerifyTargetGroup)

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
				Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("shared")}},
			}},
		}, nil)
		verifyDescribe := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
			LoadBalancers: loadBalancers,
		}, nil)
		verifyUnownedTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
			TagDescriptions: []elbv2types.TagDescription{{
				ResourceArn: aws.String(ownedARN),
				Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("shared")}},
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
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
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
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
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

func TestDeleteNamedV2LoadBalancerContinuesAfterDeletionFailures(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBV2API(ctrl)

	const (
		vpcID             = "vpc-owned"
		infraID           = "infra-id"
		loadBalancerARN   = "arn:load-balancer"
		listenerARNOne    = "arn:listener-one"
		listenerARNTwo    = "arn:listener-two"
		targetGroupARNOne = "arn:target-group-one"
		targetGroupARNTwo = "arn:target-group-two"
	)
	selector := LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}
	listenerErr := errors.New("listener deletion failed")
	targetGroupErr := errors.New("target group deletion failed")
	loadBalancerErr := errors.New("load balancer deletion failed")

	describeTargetGroups := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{
			{TargetGroupArn: aws.String(targetGroupARNOne), VpcId: aws.String(vpcID)},
			{TargetGroupArn: aws.String(targetGroupARNTwo), VpcId: aws.String(vpcID)},
		},
	}, nil)
	describeTargetGroupOneTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARNOne},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARNOne),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	describeTargetGroupTwoTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARNTwo},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARNTwo),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	describeListeners := client.EXPECT().DescribeListeners(gomock.Any(), &elasticloadbalancingv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{
		Listeners: []elbv2types.Listener{
			{ListenerArn: aws.String(listenerARNOne)},
			{ListenerArn: aws.String(listenerARNTwo)},
		},
	}, nil)
	deleteListenerOne := client.EXPECT().DeleteListener(gomock.Any(), &elasticloadbalancingv2.DeleteListenerInput{
		ListenerArn: aws.String(listenerARNOne),
	}, gomock.Any()).Return(nil, listenerErr)
	deleteListenerTwo := client.EXPECT().DeleteListener(gomock.Any(), &elasticloadbalancingv2.DeleteListenerInput{
		ListenerArn: aws.String(listenerARNTwo),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteListenerOutput{}, nil)
	deleteTargetGroupOne := client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARNOne),
	}, gomock.Any()).Return(nil, targetGroupErr)
	deleteTargetGroupTwo := client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARNTwo),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	deleteLoadBalancer := client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(nil, loadBalancerErr)
	gomock.InOrder(describeTargetGroups, describeTargetGroupOneTags, describeTargetGroupTwoTags, describeListeners, deleteListenerOne, deleteListenerTwo, deleteTargetGroupOne, deleteTargetGroupTwo, deleteLoadBalancer)

	targetGroupARNs, err := deleteNamedV2LoadBalancer(context.Background(), client, elbv2types.LoadBalancer{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, selector, logr.Discard())
	if !errors.Is(err, listenerErr) || !errors.Is(err, targetGroupErr) || !errors.Is(err, loadBalancerErr) {
		t.Fatalf("expected all deletion errors to be returned, got %v", err)
	}
	if len(targetGroupARNs) != 2 {
		t.Fatalf("expected both target groups to be retained for verification, got %v", targetGroupARNs)
	}
}

func TestDeleteOwnedV2TargetGroupsContinuesAfterPaginationFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBV2API(ctrl)

	const (
		vpcID          = "vpc-owned"
		infraID        = "infra-id"
		targetGroupARN = "arn:target-group"
	)
	pageErr := errors.New("second target group page failed")

	firstPage := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{}, gomock.Any()).
		Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
			TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
			NextMarker:   aws.String("next-page"),
		}, nil)
	ownedTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	secondPage := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		Marker: aws.String("next-page"),
	}, gomock.Any()).Return(nil, pageErr)
	deleteTargetGroup := client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(targetGroupARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	gomock.InOrder(firstPage, ownedTags, secondPage, deleteTargetGroup)

	arns, errs := deleteOwnedV2TargetGroups(context.Background(), client, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, logr.Discard())
	if len(errs) != 1 || !errors.Is(errs[0], pageErr) {
		t.Fatalf("expected the pagination error to be reported, got %v", errs)
	}
	if !arns.Has(targetGroupARN) {
		t.Fatalf("expected the previously discovered target group %q to be tracked", targetGroupARN)
	}
}

func TestDeleteNamedV2LoadBalancerDoesNotDeleteSharedTargetGroups(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBV2API(ctrl)
	const (
		infraID         = "infra-id"
		vpcID           = "vpc-owned"
		loadBalancerARN = "arn:load-balancer"
		targetGroupARN  = "arn:target-group"
	)
	loadBalancer := elbv2types.LoadBalancer{
		LoadBalancerArn: aws.String(loadBalancerARN),
		VpcId:           aws.String(vpcID),
	}

	describeTargetGroups := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	describeTargetGroupTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("shared")}},
		}},
	}, nil)
	describeListeners := client.EXPECT().DescribeListeners(gomock.Any(), &elasticloadbalancingv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{}, nil)
	client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	deleteLoadBalancer := client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
	gomock.InOrder(describeTargetGroups, describeTargetGroupTags, describeListeners, deleteLoadBalancer)

	targetGroupARNs, err := deleteNamedV2LoadBalancer(context.Background(), client, loadBalancer, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, logr.Discard())
	if err != nil {
		t.Fatalf("expected no errors, got %v", err)
	}
	if len(targetGroupARNs) != 0 {
		t.Fatalf("expected shared target group to be excluded from deletion, got %v", targetGroupARNs)
	}
}

func TestDeleteLoadBalancersByNameCleansOrphanedTargetGroups(t *testing.T) {
	tests := []struct {
		name               string
		tagValue           string
		loadBalancerExists bool
		loadBalancerARNs   []string
		deleteGroup        bool
	}{
		{
			name:        "When an orphaned target group is owned, it should be deleted",
			tagValue:    "owned",
			deleteGroup: true,
		},
		{
			name:               "When a named load balancer is deleted, it should also remove its orphaned target groups",
			tagValue:           "owned",
			loadBalancerExists: true,
			deleteGroup:        true,
		},
		{
			name:     "When an orphaned target group is shared, it should not be deleted",
			tagValue: "shared",
		},
		{
			name:             "When a target group is still attached, it should not be deleted",
			loadBalancerARNs: []string{"arn:load-balancer"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := awsapi.NewMockELBV2API(ctrl)
			const (
				name            = "cluster-lb"
				infraID         = "infra-id"
				vpcID           = "vpc-owned"
				loadBalancerARN = "arn:load-balancer"
				targetGroupARN  = "arn:target-group"
			)
			calls := []any{}
			if test.loadBalancerExists {
				describeLoadBalancer := client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
					Names: []string{name},
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
					LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(loadBalancerARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
				}, nil)
				describeLoadBalancerTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
					ResourceArns: []string{loadBalancerARN},
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
					TagDescriptions: []elbv2types.TagDescription{{
						ResourceArn: aws.String(loadBalancerARN),
						Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
					}},
				}, nil)
				describeTargetGroupsForLoadBalancer := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
					LoadBalancerArn: aws.String(loadBalancerARN),
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)
				describeListeners := client.EXPECT().DescribeListeners(gomock.Any(), &elasticloadbalancingv2.DescribeListenersInput{
					LoadBalancerArn: aws.String(loadBalancerARN),
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{}, nil)
				deleteLoadBalancer := client.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
					LoadBalancerArn: aws.String(loadBalancerARN),
				}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
				calls = append(calls, describeLoadBalancer, describeLoadBalancerTags, describeTargetGroupsForLoadBalancer, describeListeners, deleteLoadBalancer)
			} else {
				describeMissingLoadBalancer := client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
					Names: []string{name},
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil)
				calls = append(calls, describeMissingLoadBalancer)
			}
			listTargetGroups := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
				TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID), LoadBalancerArns: test.loadBalancerARNs}},
			}, nil)
			calls = append(calls, listTargetGroups)
			if len(test.loadBalancerARNs) == 0 {
				describeTargetGroupTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
					ResourceArns: []string{targetGroupARN},
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
					TagDescriptions: []elbv2types.TagDescription{{
						ResourceArn: aws.String(targetGroupARN),
						Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String(test.tagValue)}},
					}},
				}, nil)
				calls = append(calls, describeTargetGroupTags)
			}
			if test.deleteGroup {
				calls = append(calls, client.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
					TargetGroupArn: aws.String(targetGroupARN),
				}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil))
			} else {
				client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			}
			calls = append(calls, client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
				Names: []string{name},
			}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil))
			if test.deleteGroup {
				calls = append(calls, client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
					TargetGroupArns: []string{targetGroupARN},
				}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil))
			}
			gomock.InOrder(calls...)

			removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELBV2: client}, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
			if err != nil {
				t.Fatalf("expected orphaned target group cleanup to complete, got %v", err)
			}
			if !removed {
				t.Fatal("expected cleanup to complete after ignoring shared or attached target groups and deleting owned orphans")
			}
		})
	}
}

func TestDeleteNamedClassicLoadBalancersWhenTagLookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBAPI(ctrl)
	const (
		name    = "cluster-lb"
		vpcID   = "vpc-owned"
		infraID = "infra-id"
	)
	client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
		LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: aws.String(name), VPCId: aws.String(vpcID)}},
	}, nil)
	client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("tag service unavailable"))
	client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	errs := deleteNamedClassicLoadBalancers(context.Background(), client, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "failed to inspect ELB tags") {
		t.Fatalf("expected tag lookup error and no deletion, got %v", errs)
	}
}

func TestDeleteNamedV2LoadBalancersWhenTagLookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBV2API(ctrl)
	const (
		name            = "cluster-lb"
		vpcID           = "vpc-owned"
		infraID         = "infra-id"
		loadBalancerARN = "arn:load-balancer"
	)
	client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(loadBalancerARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
	}, nil)
	client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("tag service unavailable"))
	client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	_, errs := deleteNamedV2LoadBalancers(context.Background(), client, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "failed to inspect ELBV2 load balancer tags") {
		t.Fatalf("expected tag lookup error and no deletion, got %v", errs)
	}
}

func TestDeleteLoadBalancersByNameWaitsForEventualConsistency(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBAPI(ctrl)
	const (
		name    = "cluster-lb"
		vpcID   = "vpc-owned"
		infraID = "infra-id"
	)
	loadBalancer := elbtypes.LoadBalancerDescription{LoadBalancerName: aws.String(name), VPCId: aws.String(vpcID)}
	tagOutput := &elasticloadbalancing.DescribeTagsOutput{TagDescriptions: []elbtypes.TagDescription{{
		LoadBalancerName: aws.String(name),
		Tags:             []elbtypes.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
	}}}

	describeForDelete := client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
		LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{loadBalancer},
	}, nil)
	tagsForDelete := client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(tagOutput, nil)
	deleteLoadBalancer := client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)
	describeAfterDelete := client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeLoadBalancersOutput{
		LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{loadBalancer},
	}, nil)
	tagsAfterDelete := client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(tagOutput, nil)
	gomock.InOrder(describeForDelete, tagsForDelete, deleteLoadBalancer, describeAfterDelete, tagsAfterDelete)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELB: client}, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
	if err != nil {
		t.Fatalf("expected eventual consistency to be reported without an error, got %v", err)
	}
	if removed {
		t.Fatal("expected cleanup to remain incomplete while AWS still reports the load balancer")
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
			Tags:             []elbtypes.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
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
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	v2TargetGroups := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2TargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	v2Listeners := elbv2Client.EXPECT().DescribeListeners(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{}, nil)
	v2DeleteTargetGroup := elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, targetGroupNotFound)
	v2DeleteLoadBalancer := elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)
	v2OrphanTargetGroupScan := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)
	v2VerifyLoadBalancer := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, notFound)
	// The initial target-group lookup returns the target group; the verification lookup observes its deletion.
	v2VerifyTargetGroup := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, targetGroupNotFound)
	gomock.InOrder(classicDescribe, classicTags, classicDelete, v2Describe, v2Tags, v2TargetGroups, v2TargetGroupTags, v2Listeners, v2DeleteTargetGroup, v2DeleteLoadBalancer, v2OrphanTargetGroupScan, classicVerify, v2VerifyLoadBalancer, v2VerifyTargetGroup)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELB: elbClient, ELBV2: elbv2Client}, selector, []string{name}, logr.Discard())
	if err != nil {
		t.Fatalf("expected not-found deletes to be idempotent, got %v", err)
	}
	if !removed {
		t.Fatal("expected named load balancer resources to be removed")
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "When AWS reports a load balancer is missing, it should treat the error as not found",
			err:  &smithy.GenericAPIError{Code: "LoadBalancerNotFound"},
			want: true,
		},
		{
			name: "When AWS reports a target group is missing, it should treat the error as not found",
			err:  &smithy.GenericAPIError{Code: "TargetGroupNotFound"},
			want: true,
		},
		{
			name: "When AWS reports a listener is missing, it should treat the error as not found",
			err:  &smithy.GenericAPIError{Code: "ListenerNotFound"},
			want: true,
		},
		{
			name: "When a not-found API error is wrapped, it should still be recognized",
			err:  errors.Join(&smithy.GenericAPIError{Code: "TargetGroupNotFound"}),
			want: true,
		},
		{
			name: "When another API code contains NotFound, it should not be treated as not found",
			err:  &smithy.GenericAPIError{Code: "SomeResourceNotFound"},
			want: false,
		},
		{
			name: "When AWS reports a different API error, it should not be treated as not found",
			err:  &smithy.GenericAPIError{Code: "ResourceInUse"},
			want: false,
		},
		{
			name: "When the error is not an AWS API error, it should not be treated as not found",
			err:  errors.New("not found"),
			want: false,
		},
		{
			name: "When there is no error, it should not be treated as not found",
			err:  nil,
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isNotFound(test.err); got != test.want {
				t.Fatalf("isNotFound(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestDeleteLoadBalancersByNameScansOrphansAfterTargetGroupDeletionFails(t *testing.T) {
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
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	v2TargetGroups := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2TargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	v2Listeners := elbv2Client.EXPECT().DescribeListeners(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeListenersOutput{
		Listeners: []elbv2types.Listener{{ListenerArn: aws.String(listenerARN)}},
	}, nil)
	v2DeleteListener := elbv2Client.EXPECT().DeleteListener(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DeleteListenerOutput{}, nil)
	targetGroupErr := errors.New("target group is still in use")
	v2DeleteTargetGroup := elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, targetGroupErr)
	v2DeleteLoadBalancer := elbv2Client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
	v2OrphanTargetGroupScan := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	v2OrphanTargetGroupTags := elbv2Client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
		TagDescriptions: []elbv2types.TagDescription{{
			ResourceArn: aws.String(targetGroupARN),
			Tags:        []elbv2types.Tag{{Key: aws.String("kubernetes.io/cluster/" + infraID), Value: aws.String("owned")}},
		}},
	}, nil)
	v2RetryDeleteTargetGroup := elbv2Client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
	v2VerifyLoadBalancer := elbv2Client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "LoadBalancerNotFound", Message: "not found"})
	v2VerifyTargetGroup := elbv2Client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{targetGroupARN},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil)
	gomock.InOrder(v2Describe, v2Tags, v2TargetGroups, v2TargetGroupTags, v2Listeners, v2DeleteListener, v2DeleteTargetGroup, v2DeleteLoadBalancer, v2OrphanTargetGroupScan, v2OrphanTargetGroupTags, v2RetryDeleteTargetGroup, v2VerifyLoadBalancer, v2VerifyTargetGroup)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELBV2: elbv2Client}, selector, []string{name}, logr.Discard())
	if removed {
		t.Fatal("expected cleanup to remain incomplete")
	}
	if !errors.Is(err, targetGroupErr) || !strings.Contains(err.Error(), "target group is still in use") {
		t.Fatalf("expected target group deletion error to remain visible after orphan cleanup, got %v", err)
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
			name:     "When the hostname uses the NLB format, it should remove the generated suffix",
			hostname: "cluster-lb-123456abcdef.elb.us-east-1.amazonaws.com",
			want:     "cluster-lb",
		},
		{
			name:     "When the hostname has an availability-zone prefix, it should extract the load balancer label",
			hostname: "us-east-2b.cluster-lb-123456abcdef.elb.us-east-2.amazonaws.com",
			want:     "cluster-lb",
		},
		{
			name:     "When an E2E ELB hostname contains a multi-segment name, it should remove only the generated suffix",
			hostname: "e2e-v7-fnt8p-ext-9a316db0952d7e14.elb.us-east-1.amazonaws.com",
			want:     "e2e-v7-fnt8p-ext",
		},
		{
			name:     "When an E2E NLB hostname uses a numeric suffix, it should remove only the generated suffix",
			hostname: "a7f9d8c870a2b44c39d9565e2ec22e81-1194117244.us-east-1.elb.amazonaws.com",
			want:     "a7f9d8c870a2b44c39d9565e2ec22e81",
		},
		{
			name:     "When the hostname uses the AWS China classic format, it should remove the generated suffix",
			hostname: "cluster-lb-123456.cn-north-1.elb.amazonaws.com.cn",
			want:     "cluster-lb",
		},
		{
			name:     "When the hostname uses the AWS China NLB format, it should remove the generated suffix",
			hostname: "cluster-lb-123456abcdef.elb.cn-north-1.amazonaws.com.cn",
			want:     "cluster-lb",
		},
		{
			name:     "When the hostname uses the AWS GovCloud format, it should remove the generated suffix",
			hostname: "cluster-lb-123456.us-gov-west-1.elb.amazonaws.com",
			want:     "cluster-lb",
		},
		{
			name:     "When the hostname is not an AWS load balancer hostname, it should return an empty name",
			hostname: "cluster-lb-123456.example.com",
			want:     "",
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

func TestLoadBalancerRegionFromHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{
			name:     "When the hostname uses the classic ELB format, it should return its region",
			hostname: "internal-cluster-lb-123456.us-east-1.elb.amazonaws.com",
			want:     "us-east-1",
		},
		{
			name:     "When the hostname uses the NLB format, it should return its region",
			hostname: "cluster-lb-123456abcdef.elb.us-west-2.amazonaws.com",
			want:     "us-west-2",
		},
		{
			name:     "When an NLB hostname has an availability-zone prefix, it should return its region",
			hostname: "us-west-2a.cluster-lb-123456abcdef.elb.us-west-2.amazonaws.com",
			want:     "us-west-2",
		},
		{
			name:     "When a classic ELB hostname has an availability-zone prefix, it should return its region",
			hostname: "us-east-1a.internal-cluster-lb-123456.us-east-1.elb.amazonaws.com",
			want:     "us-east-1",
		},
		{
			name:     "When the hostname uses the AWS China format, it should return its region",
			hostname: "cluster-lb-123456.cn-north-1.elb.amazonaws.com.cn",
			want:     "cn-north-1",
		},
		{
			name:     "When the hostname is not an AWS load balancer hostname, it should return an empty region",
			hostname: "lb.example.com",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := LoadBalancerRegionFromHostname(test.hostname); got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}

func TestLoadBalancerTagLookupRejectsAccessDenied(t *testing.T) {
	accessDenied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}

	t.Run("When classic ELB tag lookup is denied, it should refuse to assume ownership", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := awsapi.NewMockELBAPI(ctrl)
		client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, accessDenied)

		owned, err := classicLoadBalancerHasClusterTag(context.Background(), client, aws.String("cluster-lb"), "infra-id")
		if err == nil || !strings.Contains(err.Error(), "delegated role needs elasticloadbalancing:DescribeTags") {
			t.Fatalf("expected access denial to prevent ownership verification, got %v", err)
		}
		if owned {
			t.Fatal("expected an ELB with unreadable ownership tags not to be considered owned")
		}
	})

	t.Run("When ELBv2 tag lookup is denied, it should refuse to assume ownership", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := awsapi.NewMockELBV2API(ctrl)
		client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, accessDenied)

		owned, err := v2ResourceHasClusterTag(context.Background(), client, aws.String("arn:load-balancer"), "infra-id")
		if err == nil || !strings.Contains(err.Error(), "delegated role needs elasticloadbalancing:DescribeTags") {
			t.Fatalf("expected access denial to prevent ownership verification, got %v", err)
		}
		if owned {
			t.Fatal("expected an ELBv2 resource with unreadable ownership tags not to be considered owned")
		}
	})
}

func TestIsOnlyDescribeTagsAccessDenied(t *testing.T) {
	underlying := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "request to https://private-endpoint.example was denied"}
	tagDenied := &describeTagsAccessDeniedError{err: underlying}

	if !errors.Is(tagDenied, ErrDescribeTagsAccessDenied) {
		t.Fatal("expected DescribeTags denial to retain its classification")
	}
	if !errors.Is(tagDenied, underlying) {
		t.Fatal("expected DescribeTags denial to preserve the underlying AWS error")
	}
	if !IsOnlyDescribeTagsAccessDenied(errors.Join(tagDenied, tagDenied)) {
		t.Fatal("expected a join containing only DescribeTags denials to be eligible for fallback")
	}
	if !IsOnlyDescribeTagsAccessDenied(fmt.Errorf("cleanup failed: %w", tagDenied)) {
		t.Fatal("expected a wrapped DescribeTags denial to remain eligible for fallback")
	}
	if IsOnlyDescribeTagsAccessDenied(errors.Join(tagDenied, errors.New("DeleteLoadBalancer failed"))) {
		t.Fatal("expected a mixed tag-verification and delete failure not to be eligible for fallback")
	}
	if IsOnlyDescribeTagsAccessDenied(fmt.Errorf("cleanup failed: %w", errors.Join(tagDenied, errors.New("DeleteLoadBalancer failed")))) {
		t.Fatal("expected a wrapped mixed failure not to be eligible for fallback")
	}
}

func TestDeleteLoadBalancersByNameRetainsResourcesWhenTagLookupIsDenied(t *testing.T) {
	t.Run("When ownership tags are inaccessible in a shared VPC, it should not delete the load balancer", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := awsapi.NewMockELBAPI(ctrl)
		const (
			name    = "cluster-lb"
			infraID = "infra-id"
			vpcID   = "vpc-shared"
		)
		accessDenied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
		loadBalancer := &elasticloadbalancing.DescribeLoadBalancersOutput{
			LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: aws.String(name), VPCId: aws.String(vpcID)}},
		}
		describeForDelete := client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(loadBalancer, nil)
		tagsForDelete := client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, accessDenied)
		client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		describeForVerification := client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(loadBalancer, nil)
		tagsForVerification := client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, accessDenied)
		gomock.InOrder(describeForDelete, tagsForDelete, describeForVerification, tagsForVerification)

		removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELB: client}, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
		if removed {
			t.Fatal("expected cleanup to remain incomplete when ownership tags cannot be read")
		}
		if err == nil || !strings.Contains(err.Error(), "delegated role needs elasticloadbalancing:DescribeTags") {
			t.Fatalf("expected ownership verification error, got %v", err)
		}
	})

	t.Run("When ELBv2 ownership tags are inaccessible in a shared VPC, it should not delete the load balancer", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := awsapi.NewMockELBV2API(ctrl)
		const (
			name            = "cluster-lb"
			infraID         = "infra-id"
			vpcID           = "vpc-shared"
			loadBalancerARN = "arn:load-balancer"
		)
		accessDenied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
		loadBalancer := &elasticloadbalancingv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(loadBalancerARN), LoadBalancerName: aws.String(name), VpcId: aws.String(vpcID)}},
		}
		describeForDelete := client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(loadBalancer, nil)
		tagsForDelete := client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, accessDenied)
		client.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		describeForVerification := client.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(loadBalancer, nil)
		tagsForVerification := client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, accessDenied)
		gomock.InOrder(describeForDelete, tagsForDelete, describeForVerification, tagsForVerification)

		removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELBV2: client}, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
		if removed {
			t.Fatal("expected cleanup to remain incomplete when ownership tags cannot be read")
		}
		if err == nil || !strings.Contains(err.Error(), "delegated role needs elasticloadbalancing:DescribeTags") {
			t.Fatalf("expected ownership verification error, got %v", err)
		}
	})
}

func TestDeleteLoadBalancersByNameDoesNotDeleteOrphanTargetGroupsWhenTagLookupIsDenied(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := awsapi.NewMockELBV2API(ctrl)
	const (
		name           = "cluster-lb"
		infraID        = "infra-id"
		vpcID          = "vpc-shared"
		targetGroupARN = "arn:target-group"
	)
	accessDenied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
	describeMissingLoadBalancer := client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil)
	listTargetGroups := client.EXPECT().DescribeTargetGroups(gomock.Any(), &elasticloadbalancingv2.DescribeTargetGroupsInput{}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeTargetGroupsOutput{
		TargetGroups: []elbv2types.TargetGroup{{TargetGroupArn: aws.String(targetGroupARN), VpcId: aws.String(vpcID)}},
	}, nil)
	describeTargetGroupTags := client.EXPECT().DescribeTags(gomock.Any(), &elasticloadbalancingv2.DescribeTagsInput{
		ResourceArns: []string{targetGroupARN},
	}, gomock.Any()).Return(nil, accessDenied)
	client.EXPECT().DeleteTargetGroup(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	verifyLoadBalancer := client.EXPECT().DescribeLoadBalancers(gomock.Any(), &elasticloadbalancingv2.DescribeLoadBalancersInput{
		Names: []string{name},
	}, gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil)
	gomock.InOrder(describeMissingLoadBalancer, listTargetGroups, describeTargetGroupTags, verifyLoadBalancer)

	removed, err := DeleteLoadBalancersByName(context.Background(), LoadBalancerClients{ELBV2: client}, LoadBalancerSelector{VPCID: vpcID, InfraID: infraID}, []string{name}, logr.Discard())
	if removed {
		t.Fatal("expected cleanup to remain incomplete when target group ownership tags cannot be read")
	}
	if err == nil || !strings.Contains(err.Error(), "delegated role needs elasticloadbalancing:DescribeTags") {
		t.Fatalf("expected target group ownership verification error, got %v", err)
	}
}

func TestLoadBalancerTagLookupRequiresOwnedValue(t *testing.T) {
	t.Run("When a classic load balancer has a shared cluster tag, it should not be considered owned", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := awsapi.NewMockELBAPI(ctrl)
		client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancing.DescribeTagsOutput{
			TagDescriptions: []elbtypes.TagDescription{{
				LoadBalancerName: aws.String("cluster-lb"),
				Tags: []elbtypes.Tag{{
					Key:   aws.String("kubernetes.io/cluster/infra-id"),
					Value: aws.String("shared"),
				}},
			}},
		}, nil)

		owned, err := classicLoadBalancerHasClusterTag(context.Background(), client, aws.String("cluster-lb"), "infra-id")
		if err != nil {
			t.Fatalf("expected no tag lookup error, got %v", err)
		}
		if owned {
			t.Fatal("expected a shared load balancer tag not to indicate cluster ownership")
		}
	})

	t.Run("When an ELBv2 resource has a shared cluster tag, it should not be considered owned", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := awsapi.NewMockELBV2API(ctrl)
		client.EXPECT().DescribeTags(gomock.Any(), gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeTagsOutput{
			TagDescriptions: []elbv2types.TagDescription{{
				ResourceArn: aws.String("arn:load-balancer"),
				Tags: []elbv2types.Tag{{
					Key:   aws.String("kubernetes.io/cluster/infra-id"),
					Value: aws.String("shared"),
				}},
			}},
		}, nil)

		owned, err := v2ResourceHasClusterTag(context.Background(), client, aws.String("arn:load-balancer"), "infra-id")
		if err != nil {
			t.Fatalf("expected no tag lookup error, got %v", err)
		}
		if owned {
			t.Fatal("expected a shared ELBv2 resource tag not to indicate cluster ownership")
		}
	})
}
