package aws

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsapi"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"go.uber.org/mock/gomock"
)

func TestReconcileAWSEndpointServiceStatus(t *testing.T) {
	const mockControlPlaneOperatorRoleArn = "arn:aws:12345678910::iam:role/fakeRoleARN"

	tests := []struct {
		name                        string
		additionalAllowedPrincipals []string
		existingAllowedPrincipals   []string
		expectedPrincipalsToAdd     []string
		expectedPrincipalsToRemove  []string
	}{
		{
			name:                    "no additional principals",
			expectedPrincipalsToAdd: []string{mockControlPlaneOperatorRoleArn},
		},
		{
			name:                        "additional principals",
			additionalAllowedPrincipals: []string{"additional1", "additional2"},
			expectedPrincipalsToAdd:     []string{mockControlPlaneOperatorRoleArn, "additional1", "additional2"},
		},
		{
			name:                       "removing extra principals",
			existingAllowedPrincipals:  []string{"existing1", "existing2"},
			expectedPrincipalsToAdd:    []string{mockControlPlaneOperatorRoleArn},
			expectedPrincipalsToRemove: []string{"existing1", "existing2"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			elbClient := awsapi.NewMockELBV2API(ctrl)
			elbClient.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any()).Return(&elasticloadbalancingv2.DescribeLoadBalancersOutput{LoadBalancers: []elbv2types.LoadBalancer{{
				LoadBalancerArn: aws.String("lb-arn"),
				State:           &elbv2types.LoadBalancerState{Code: elbv2types.LoadBalancerStateEnumActive},
			}}}, nil)

			infra := &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.InfrastructureStatus{InfrastructureName: "management-cluster-infra-id"},
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(infra).Build()

			existingAllowedPrincipals := make([]ec2types.AllowedPrincipal, len(test.existingAllowedPrincipals))
			for i, p := range test.existingAllowedPrincipals {
				existingAllowedPrincipals[i] = ec2types.AllowedPrincipal{Principal: aws.String(p)}
			}

			mockEC2 := awsapi.NewMockEC2API(ctrl)

			var created *ec2.CreateVpcEndpointServiceConfigurationInput
			mockEC2.EXPECT().CreateVpcEndpointServiceConfiguration(gomock.Any(), gomock.Any()).
				Do(func(_ context.Context, in *ec2.CreateVpcEndpointServiceConfigurationInput, _ ...func(*ec2.Options)) {
					created = in
				}).
				Return(&ec2.CreateVpcEndpointServiceConfigurationOutput{ServiceConfiguration: &ec2types.ServiceConfiguration{ServiceName: aws.String("ep-service")}}, nil)

			mockEC2.EXPECT().DescribeVpcEndpointServicePermissions(gomock.Any(), gomock.Any()).Return(
				&ec2.DescribeVpcEndpointServicePermissionsOutput{AllowedPrincipals: existingAllowedPrincipals}, nil)

			var setPerms *ec2.ModifyVpcEndpointServicePermissionsInput
			mockEC2.EXPECT().ModifyVpcEndpointServicePermissions(gomock.Any(), gomock.Any()).
				Do(func(_ context.Context, in *ec2.ModifyVpcEndpointServicePermissionsInput, _ ...func(*ec2.Options)) {
					setPerms = in
				}).
				Return(&ec2.ModifyVpcEndpointServicePermissionsOutput{}, nil)

			r := AWSEndpointServiceReconciler{Client: client}

			if err := r.reconcileAWSEndpointServiceStatus(t.Context(), &hyperv1.AWSEndpointService{}, &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							AdditionalAllowedPrincipals: test.additionalAllowedPrincipals,
							RolesRef: hyperv1.AWSRolesRef{
								ControlPlaneOperatorARN: mockControlPlaneOperatorRoleArn,
							},
						},
					},
				},
			}, mockEC2, elbClient); err != nil {
				t.Fatalf("reconcileAWSEndpointServiceStatus failed: %v", err)
			}

			if actual, expected := aws.ToString(created.TagSpecifications[0].Tags[0].Key), "kubernetes.io/cluster/management-cluster-infra-id"; actual != expected {
				t.Errorf("expected first tag key to be %s, was %s", expected, actual)
			}

			if actual, expected := aws.ToString(created.TagSpecifications[0].Tags[0].Value), "owned"; actual != expected {
				t.Errorf("expected first tags value to be %s, was %s", expected, actual)
			}

			actualToAdd := map[string]struct{}{mockControlPlaneOperatorRoleArn: {}}
			for _, arn := range setPerms.AddAllowedPrincipals {
				actualToAdd[arn] = struct{}{}
			}

			for _, arn := range test.expectedPrincipalsToAdd {
				if _, ok := actualToAdd[arn]; !ok {
					t.Errorf("expected %v to be added as allowed principals, actual: %v", test.expectedPrincipalsToAdd, actualToAdd)
				}
			}

			actualToRemove := map[string]struct{}{}
			for _, arn := range setPerms.RemoveAllowedPrincipals {
				actualToRemove[arn] = struct{}{}
			}

			for _, arn := range test.expectedPrincipalsToRemove {
				if _, ok := actualToRemove[arn]; !ok {
					t.Errorf("expected %v to be added as allowed principals, actual: %v", test.expectedPrincipalsToRemove, actualToRemove)
				}
			}
		})
	}
}

func TestDeleteAWSEndpointService(t *testing.T) {
	existingConnectionsDeleteOut := &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
		Unsuccessful: []ec2types.UnsuccessfulItem{
			{
				Error: &ec2types.UnsuccessfulItemError{
					Code:    aws.String("ExistingVpcEndpointConnections"),
					Message: aws.String("Service has existing active VPC Endpoint connections!"),
				},
				ResourceId: aws.String("vpce-svc-id"),
			},
		},
	}

	tests := []struct {
		name         string
		deleteOut    *ec2.DeleteVpcEndpointServiceConfigurationsOutput
		deleteErr    error
		describeOut  *ec2.DescribeVpcEndpointConnectionsOutput
		describeErr  error
		expectReject bool
		rejectErr    error
		expected     bool
		expectErr    bool
	}{
		{
			name: "When deletion succeeds it should return completed",
			deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
				Unsuccessful: []ec2types.UnsuccessfulItem{},
			},
			expected:  true,
			expectErr: false,
		},
		{
			name: "When endpoint service no longer exists it should return completed",
			deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
				Unsuccessful: []ec2types.UnsuccessfulItem{
					{
						Error: &ec2types.UnsuccessfulItemError{
							Code:    aws.String("InvalidVpcEndpointService.NotFound"),
							Message: aws.String("The VpcEndpointService Id 'vpce-svc-id' does not exist"),
						},
						ResourceId: aws.String("vpce-svc-id"),
					},
				},
			},
			expected:  true,
			expectErr: false,
		},
		{
			name:      "When existing connections are in Available state it should reject them",
			deleteOut: existingConnectionsDeleteOut,
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-id"),
						VpcEndpointState: ec2types.StateAvailable,
					},
				},
			},
			expectReject: true,
			expected:     false,
			expectErr:    true,
		},
		{
			name:      "When existing connections are in Rejected state it should not reject and return not completed without error",
			deleteOut: existingConnectionsDeleteOut,
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-id"),
						VpcEndpointState: ec2types.StateRejected,
					},
				},
			},
			expectReject: false,
			expected:     false,
			expectErr:    false,
		},
		{
			name:      "When existing connections are in Deleting state it should not reject and return not completed without error",
			deleteOut: existingConnectionsDeleteOut,
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-id"),
						VpcEndpointState: ec2types.StateDeleting,
					},
				},
			},
			expectReject: false,
			expected:     false,
			expectErr:    false,
		},
		{
			name:      "When existing connections are a mix of Available and Rejected it should reject the Available ones and return not completed without error",
			deleteOut: existingConnectionsDeleteOut,
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-available"),
						VpcEndpointState: ec2types.StateAvailable,
					},
					{
						VpcEndpointId:    aws.String("vpce-rejected"),
						VpcEndpointState: ec2types.StateRejected,
					},
					{
						VpcEndpointId:    aws.String("vpce-deleting"),
						VpcEndpointState: ec2types.StateDeleting,
					},
				},
			},
			expectReject: true,
			expected:     false,
			expectErr:    false,
		},
		{
			name:      "When existing connections are all in terminal states it should return not completed with error",
			deleteOut: existingConnectionsDeleteOut,
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-deleted"),
						VpcEndpointState: ec2types.StateDeleted,
					},
					{
						VpcEndpointId:    aws.String("vpce-failed"),
						VpcEndpointState: ec2types.StateFailed,
					},
				},
			},
			expectReject: false,
			expected:     false,
			expectErr:    true,
		},
		{
			name:      "When DeleteVpcEndpointServiceConfigurations returns an API error it should attempt to reject and return error",
			deleteErr: fmt.Errorf("aws api error"),
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{},
			},
			expected:  false,
			expectErr: true,
		},
		{
			name:        "When DescribeVpcEndpointConnections fails it should return error",
			deleteOut:   existingConnectionsDeleteOut,
			describeErr: fmt.Errorf("describe connections error"),
			expected:    false,
			expectErr:   true,
		},
		{
			name:      "When RejectVpcEndpointConnections fails it should return error",
			deleteOut: existingConnectionsDeleteOut,
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-id"),
						VpcEndpointState: ec2types.StateAvailable,
					},
				},
			},
			expectReject: true,
			rejectErr:    fmt.Errorf("reject connections error"),
			expected:     false,
			expectErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
			mockEC2.EXPECT().DeleteVpcEndpointServiceConfigurations(gomock.Any(), gomock.Any()).Return(test.deleteOut, test.deleteErr)
			if test.describeOut != nil || test.describeErr != nil {
				mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(test.describeOut, test.describeErr)
				if test.expectReject {
					mockEC2.EXPECT().RejectVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(nil, test.rejectErr)
				}
			}

			obj := &hyperv1.AWSEndpointService{
				Status: hyperv1.AWSEndpointServiceStatus{EndpointServiceName: "vpce-svc-id"},
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(obj).Build()

			r := AWSEndpointServiceReconciler{
				ec2Client: mockEC2,
				Client:    client,
			}

			ctx := log.IntoContext(t.Context(), testr.New(t))
			actual, err := r.delete(ctx, obj)
			if err != nil {
				if !test.expectErr {
					t.Errorf("expected no err, got %v", err)
				}
			} else {
				if test.expectErr {
					t.Error("expected err, got nil")
				} else {
					if test.expected != actual {
						t.Errorf("expected %v, got %v", test.expected, actual)
					}
				}
			}
		})
	}
}

func TestRejectVpcEndpointConnections(t *testing.T) {
	tests := []struct {
		name                       string
		connections                []ec2types.VpcEndpointConnection
		expectRejectCall           bool
		expectedRejectIDs          []string
		expectHasTransitionalConns bool
	}{
		{
			name:                       "When no connections exist it should report no transitional connections",
			connections:                nil,
			expectRejectCall:           false,
			expectHasTransitionalConns: false,
		},
		{
			name: "When connections are in PendingAcceptance state it should reject them",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-1"), VpcEndpointState: ec2types.StatePendingAcceptance},
			},
			expectRejectCall:           true,
			expectedRejectIDs:          []string{"vpce-1"},
			expectHasTransitionalConns: false,
		},
		{
			name: "When connections are in Rejected state it should not reject and report transitional",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-1"), VpcEndpointState: ec2types.StateRejected},
			},
			expectRejectCall:           false,
			expectHasTransitionalConns: true,
		},
		{
			name: "When connections are in Deleting state it should not reject and report transitional",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-1"), VpcEndpointState: ec2types.StateDeleting},
			},
			expectRejectCall:           false,
			expectHasTransitionalConns: true,
		},
		{
			name: "When connections are in terminal states it should not reject and report no transitional",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-deleted"), VpcEndpointState: ec2types.StateDeleted},
				{VpcEndpointId: aws.String("vpce-failed"), VpcEndpointState: ec2types.StateFailed},
				{VpcEndpointId: aws.String("vpce-expired"), VpcEndpointState: ec2types.StateExpired},
			},
			expectRejectCall:           false,
			expectHasTransitionalConns: false,
		},
		{
			name: "When connections are a mix of actionable transitional and terminal states it should only reject actionable ones",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-available"), VpcEndpointState: ec2types.StateAvailable},
				{VpcEndpointId: aws.String("vpce-pending"), VpcEndpointState: ec2types.StatePending},
				{VpcEndpointId: aws.String("vpce-rejected"), VpcEndpointState: ec2types.StateRejected},
				{VpcEndpointId: aws.String("vpce-deleting"), VpcEndpointState: ec2types.StateDeleting},
				{VpcEndpointId: aws.String("vpce-deleted"), VpcEndpointState: ec2types.StateDeleted},
			},
			expectRejectCall:           true,
			expectedRejectIDs:          []string{"vpce-available", "vpce-pending"},
			expectHasTransitionalConns: true,
		},
		{
			name: "When connections are in Partial state it should treat as transitional",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-partial"), VpcEndpointState: ec2types.StatePartial},
			},
			expectRejectCall:           false,
			expectHasTransitionalConns: true,
		},
		{
			name: "When AWS API returns lowercase state values it should handle them correctly",
			connections: []ec2types.VpcEndpointConnection{
				{VpcEndpointId: aws.String("vpce-available"), VpcEndpointState: "available"},
				{VpcEndpointId: aws.String("vpce-pending"), VpcEndpointState: "pending"},
				{VpcEndpointId: aws.String("vpce-deleting"), VpcEndpointState: "deleting"},
				{VpcEndpointId: aws.String("vpce-deleted"), VpcEndpointState: "deleted"},
			},
			expectRejectCall:           true,
			expectedRejectIDs:          []string{"vpce-available", "vpce-pending"},
			expectHasTransitionalConns: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
			mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(
				&ec2.DescribeVpcEndpointConnectionsOutput{
					VpcEndpointConnections: test.connections,
				}, nil)

			if test.expectRejectCall {
				mockEC2.EXPECT().RejectVpcEndpointConnections(gomock.Any(), gomock.Any()).
					Do(func(_ context.Context, in *ec2.RejectVpcEndpointConnectionsInput, _ ...func(*ec2.Options)) {
						if len(in.VpcEndpointIds) != len(test.expectedRejectIDs) {
							t.Errorf("expected %d endpoint IDs to reject, got %d", len(test.expectedRejectIDs), len(in.VpcEndpointIds))
						}
						actualIDs := map[string]bool{}
						for _, id := range in.VpcEndpointIds {
							actualIDs[id] = true
						}
						for _, expectedID := range test.expectedRejectIDs {
							if !actualIDs[expectedID] {
								t.Errorf("expected endpoint ID %s to be rejected, but it was not", expectedID)
							}
						}
					}).
					Return(nil, nil)
			}

			r := AWSEndpointServiceReconciler{
				ec2Client: mockEC2,
			}

			ctx := log.IntoContext(t.Context(), testr.New(t))
			result, err := r.rejectVpcEndpointConnections(ctx, "vpce-svc-test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.hasTransitionalConnections != test.expectHasTransitionalConns {
				t.Errorf("expected hasTransitionalConnections=%v, got %v", test.expectHasTransitionalConns, result.hasTransitionalConnections)
			}
		})
	}

	t.Run("When DescribeVpcEndpointConnections fails it should return error", func(t *testing.T) {
		mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
		mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("describe error"))

		r := AWSEndpointServiceReconciler{
			ec2Client: mockEC2,
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		result, err := r.rejectVpcEndpointConnections(ctx, "vpce-svc-test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if result != nil {
			t.Errorf("expected nil result, got %v", result)
		}
	})

	t.Run("When RejectVpcEndpointConnections fails it should return error", func(t *testing.T) {
		mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
		mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(
			&ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{VpcEndpointId: aws.String("vpce-1"), VpcEndpointState: ec2types.StateAvailable},
				},
			}, nil)
		mockEC2.EXPECT().RejectVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("reject error"))

		r := AWSEndpointServiceReconciler{
			ec2Client: mockEC2,
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		result, err := r.rejectVpcEndpointConnections(ctx, "vpce-svc-test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if result != nil {
			t.Errorf("expected nil result, got %v", result)
		}
	})

	t.Run("When paginated results span multiple pages it should aggregate all connections", func(t *testing.T) {
		mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
		page1Token := "page2-token"
		// First page returns one actionable connection and a NextToken
		firstCall := mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(
			&ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{VpcEndpointId: aws.String("vpce-page1"), VpcEndpointState: ec2types.StateAvailable},
				},
				NextToken: &page1Token,
			}, nil)
		// Second page returns another actionable connection and a transitional one, no NextToken
		mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).After(firstCall).Return(
			&ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{VpcEndpointId: aws.String("vpce-page2"), VpcEndpointState: ec2types.StatePendingAcceptance},
					{VpcEndpointId: aws.String("vpce-page2-deleting"), VpcEndpointState: ec2types.StateDeleting},
				},
			}, nil)

		// Both actionable endpoints from both pages should be rejected in a single call
		mockEC2.EXPECT().RejectVpcEndpointConnections(gomock.Any(), gomock.Any()).
			Do(func(_ context.Context, in *ec2.RejectVpcEndpointConnectionsInput, _ ...func(*ec2.Options)) {
				if len(in.VpcEndpointIds) != 2 {
					t.Errorf("expected 2 endpoint IDs to reject, got %d", len(in.VpcEndpointIds))
				}
				actualIDs := map[string]bool{}
				for _, id := range in.VpcEndpointIds {
					actualIDs[id] = true
				}
				if !actualIDs["vpce-page1"] {
					t.Error("expected vpce-page1 to be rejected")
				}
				if !actualIDs["vpce-page2"] {
					t.Error("expected vpce-page2 to be rejected")
				}
			}).
			Return(nil, nil)

		r := AWSEndpointServiceReconciler{
			ec2Client: mockEC2,
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		result, err := r.rejectVpcEndpointConnections(ctx, "vpce-svc-test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.hasTransitionalConnections {
			t.Error("expected hasTransitionalConnections=true due to Deleting connection on page 2")
		}
	})
}

func Test_controlPlaneOperatorRoleARNWithoutPath(t *testing.T) {
	tests := []struct {
		name     string
		hc       *hyperv1.HostedCluster
		expected string
	}{
		{
			name: "ARN without path",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{
								ControlPlaneOperatorARN: "arn:aws:iam::12345678910:role/test-name",
							},
						},
					},
				},
			},
			expected: "arn:aws:iam::12345678910:role/test-name",
		},
		{
			name: "ARN with path",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{
								ControlPlaneOperatorARN: "arn:aws:iam::12345678910:role/prefix/subprefix/test-name",
							},
						},
					},
				},
			},
			expected: "arn:aws:iam::12345678910:role/test-name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := AWSEndpointServiceReconciler{}
			actual, _ := r.controlPlaneOperatorRoleARNWithoutPath(test.hc)
			if test.expected != actual {
				t.Errorf("expected: %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestListKarpenterSubnetIDs(t *testing.T) {
	testCases := []struct {
		name            string
		namespace       string
		objects         []client.Object
		expectedSubnets []string
		expectError     bool
	}{
		{
			name:            "When the ConfigMap is missing it should return empty list without error",
			namespace:       "test-namespace",
			objects:         []client.Object{},
			expectedSubnets: []string{},
		},
		{
			name:      "When a valid ConfigMap exists it should return parsed subnet IDs",
			namespace: "test-namespace",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						"subnetIDs": `["subnet-aaa","subnet-bbb","subnet-ccc"]`,
					},
				},
			},
			expectedSubnets: []string{"subnet-aaa", "subnet-bbb", "subnet-ccc"},
		},
		{
			name:      "When the ConfigMap exists with empty subnetIDs it should return empty list",
			namespace: "test-namespace",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						Namespace: "test-namespace",
					},
					Data: map[string]string{},
				},
			},
			expectedSubnets: []string{},
		},
		{
			name:      "When the ConfigMap contains malformed JSON it should return an error",
			namespace: "test-namespace",
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						"subnetIDs": `not-valid-json`,
					},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tc.objects...).
				Build()

			subnets, err := listKarpenterSubnetIDs(t.Context(), fakeClient, tc.namespace)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(subnets).To(Equal(tc.expectedSubnets))
			}
		})
	}
}

func TestListSubnetIDs(t *testing.T) {
	testCases := []struct {
		name            string
		clusterName     string
		namespace       string
		hcpNamespace    string
		objects         []client.Object
		expectedSubnets []string
	}{
		{
			name:         "When a karpenter-subnets ConfigMap exists it should include subnets from both NodePools and the ConfigMap",
			clusterName:  "my-cluster",
			namespace:    "clusters",
			hcpNamespace: "clusters-my-cluster",
			objects: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-1",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "my-cluster",
						Platform: hyperv1.NodePoolPlatform{
							AWS: &hyperv1.AWSNodePoolPlatform{
								Subnet: hyperv1.AWSResourceReference{
									ID: aws.String("subnet-nodepool"),
								},
							},
						},
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						Namespace: "clusters-my-cluster",
					},
					Data: map[string]string{
						"subnetIDs": `["subnet-karpenter-a","subnet-karpenter-b"]`,
					},
				},
			},
			expectedSubnets: []string{"subnet-karpenter-a", "subnet-karpenter-b", "subnet-nodepool"},
		},
		{
			name:         "When no karpenter-subnets ConfigMap exists it should return only NodePool subnets",
			clusterName:  "my-cluster",
			namespace:    "clusters",
			hcpNamespace: "clusters-my-cluster",
			objects: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-1",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "my-cluster",
						Platform: hyperv1.NodePoolPlatform{
							AWS: &hyperv1.AWSNodePoolPlatform{
								Subnet: hyperv1.AWSResourceReference{
									ID: aws.String("subnet-nodepool"),
								},
							},
						},
					},
				},
			},
			expectedSubnets: []string{"subnet-nodepool"},
		},
		{
			name:            "When there are no NodePools and no ConfigMap it should return an empty list",
			clusterName:     "my-cluster",
			namespace:       "clusters",
			hcpNamespace:    "clusters-my-cluster",
			objects:         []client.Object{},
			expectedSubnets: []string{},
		},
		{
			name:         "When NodePool and ConfigMap have overlapping subnets it should deduplicate",
			clusterName:  "my-cluster",
			namespace:    "clusters",
			hcpNamespace: "clusters-my-cluster",
			objects: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nodepool-1",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "my-cluster",
						Platform: hyperv1.NodePoolPlatform{
							AWS: &hyperv1.AWSNodePoolPlatform{
								Subnet: hyperv1.AWSResourceReference{
									ID: aws.String("subnet-shared"),
								},
							},
						},
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						Namespace: "clusters-my-cluster",
					},
					Data: map[string]string{
						"subnetIDs": `["subnet-shared","subnet-karpenter-only"]`,
					},
				},
			},
			expectedSubnets: []string{"subnet-karpenter-only", "subnet-shared"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tc.objects...).
				Build()

			subnets, err := listSubnetIDs(t.Context(), fakeClient, tc.clusterName, tc.namespace, tc.hcpNamespace)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(subnets).To(Equal(tc.expectedSubnets))
		})
	}
}

// captureQueue is a simple workqueue that captures added items for test inspection.
type captureQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	added []reconcile.Request
}

func (q *captureQueue) Add(item reconcile.Request) {
	q.added = append(q.added, item)
}

func TestEnqueueOnKarpenterConfigMapChange(t *testing.T) {
	testCases := []struct {
		name           string
		oldCM          *corev1.ConfigMap
		newCM          *corev1.ConfigMap
		expectedQueued int
	}{
		{
			name: "When a non-karpenter ConfigMap is updated it should not enqueue",
			oldCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-configmap",
					Namespace: "clusters-my-cluster",
				},
				Data: map[string]string{"subnetIDs": `["subnet-a"]`},
			},
			newCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-configmap",
					Namespace: "clusters-my-cluster",
				},
				Data: map[string]string{"subnetIDs": `["subnet-a","subnet-b"]`},
			},
			expectedQueued: 0,
		},
		{
			name: "When karpenter ConfigMap subnet data changes it should enqueue AWSEndpointServices",
			oldCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					Namespace: "clusters-my-cluster",
					Labels: map[string]string{
						"hypershift.openshift.io/managed-by": "karpenter",
					},
				},
				Data: map[string]string{"subnetIDs": `["subnet-a"]`},
			},
			newCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					Namespace: "clusters-my-cluster",
					Labels: map[string]string{
						"hypershift.openshift.io/managed-by": "karpenter",
					},
				},
				Data: map[string]string{"subnetIDs": `["subnet-a","subnet-b"]`},
			},
			// awsEndpointServicesByName returns 3 entries for any given namespace
			expectedQueued: 3,
		},
		{
			name: "When karpenter ConfigMap subnet data is unchanged it should not enqueue",
			oldCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					Namespace: "clusters-my-cluster",
					Labels: map[string]string{
						"hypershift.openshift.io/managed-by": "karpenter",
					},
				},
				Data: map[string]string{"subnetIDs": `["subnet-a","subnet-b"]`},
			},
			newCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					Namespace: "clusters-my-cluster",
					Labels: map[string]string{
						"hypershift.openshift.io/managed-by": "karpenter",
					},
				},
				Data: map[string]string{"subnetIDs": `["subnet-a","subnet-b"]`},
			},
			expectedQueued: 0,
		},
		{
			name: "When a ConfigMap named karpenter-subnets lacks the managed-by label it should not enqueue",
			oldCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					Namespace: "clusters-my-cluster",
				},
				Data: map[string]string{"subnetIDs": `["subnet-a"]`},
			},
			newCM: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					Namespace: "clusters-my-cluster",
				},
				Data: map[string]string{"subnetIDs": `["subnet-a","subnet-b"]`},
			},
			expectedQueued: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			mgr := &fakeManager{}

			r := &AWSEndpointServiceReconciler{}
			handler := r.enqueueOnKarpenterConfigMapChange(mgr)

			q := &captureQueue{}
			handler(t.Context(), event.UpdateEvent{
				ObjectOld: tc.oldCM,
				ObjectNew: tc.newCM,
			}, q)

			g.Expect(q.added).To(HaveLen(tc.expectedQueued))
		})
	}
}

// fakeManager implements just enough of ctrl.Manager for tests that need mgr.GetLogger().
// All unimplemented methods are delegated to the embedded nil Manager, which will
// panic if called — intentionally, as tests should never trigger those paths.
type fakeManager struct {
	ctrl.Manager
}

func (m *fakeManager) GetLogger() logr.Logger {
	return logr.Discard()
}
