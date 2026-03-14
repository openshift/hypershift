package aws

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsapi"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

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
	tests := []struct {
		name        string
		deleteOut   *ec2.DeleteVpcEndpointServiceConfigurationsOutput
		describeOut *ec2.DescribeVpcEndpointConnectionsOutput
		expected    bool
		expectErr   bool
	}{
		{
			name: "successful deletion",
			deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
				Unsuccessful: []ec2types.UnsuccessfulItem{},
			},
			expected:  true,
			expectErr: false,
		},
		{
			name: "endpoint service no longer exists",
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
			name: "existing connections",
			deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
				Unsuccessful: []ec2types.UnsuccessfulItem{
					{
						Error: &ec2types.UnsuccessfulItemError{
							Code:    aws.String("ExistingVpcEndpointConnections"),
							Message: aws.String("Service has existing active VPC Endpoint connections!"),
						},
						ResourceId: aws.String("vpce-svc-id"),
					},
				},
			},
			describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
				VpcEndpointConnections: []ec2types.VpcEndpointConnection{
					{
						VpcEndpointId:    aws.String("vpce-id"),
						VpcEndpointState: ec2types.State("available"),
					},
				},
			},
			expected:  false,
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
			mockEC2.EXPECT().DeleteVpcEndpointServiceConfigurations(gomock.Any(), gomock.Any()).Return(test.deleteOut, nil)
			if test.describeOut != nil {
				mockEC2.EXPECT().DescribeVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(test.describeOut, nil)
				mockEC2.EXPECT().RejectVpcEndpointConnections(gomock.Any(), gomock.Any()).Return(nil, nil)
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
