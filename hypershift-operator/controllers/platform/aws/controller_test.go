package aws

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/elbv2/elbv2iface"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
)

type fakeEC2Client struct {
	ec2iface.EC2API
	created     *ec2.CreateVpcEndpointServiceConfigurationInput
	createOut   *ec2.CreateVpcEndpointServiceConfigurationOutput
	deleteOut   *ec2.DeleteVpcEndpointServiceConfigurationsOutput
	describeOut *ec2.DescribeVpcEndpointConnectionsOutput
	rejectOut   *ec2.RejectVpcEndpointConnectionsOutput

	permsOut *ec2.DescribeVpcEndpointServicePermissionsOutput
	setPerms *ec2.ModifyVpcEndpointServicePermissionsInput
}

func (f *fakeEC2Client) CreateVpcEndpointServiceConfigurationWithContext(ctx aws.Context, in *ec2.CreateVpcEndpointServiceConfigurationInput, o ...request.Option) (*ec2.CreateVpcEndpointServiceConfigurationOutput, error) {
	if f.created != nil {
		return nil, errors.New("already created endpoint service")
	}
	f.created = in
	return f.createOut, nil
}

func (f *fakeEC2Client) DeleteVpcEndpointServiceConfigurationsWithContext(ctx aws.Context, in *ec2.DeleteVpcEndpointServiceConfigurationsInput, o ...request.Option) (*ec2.DeleteVpcEndpointServiceConfigurationsOutput, error) {
	return f.deleteOut, nil
}

func (f *fakeEC2Client) DescribeVpcEndpointConnectionsWithContext(ctx aws.Context, in *ec2.DescribeVpcEndpointConnectionsInput, o ...request.Option) (*ec2.DescribeVpcEndpointConnectionsOutput, error) {
	return f.describeOut, nil
}

func (f *fakeEC2Client) RejectVpcEndpointConnectionsWithContext(ctx aws.Context, in *ec2.RejectVpcEndpointConnectionsInput, o ...request.Option) (*ec2.RejectVpcEndpointConnectionsOutput, error) {
	return f.rejectOut, nil
}

type fakeElbv2Client struct {
	elbv2iface.ELBV2API
	out *elbv2.DescribeLoadBalancersOutput
}

func (f *fakeElbv2Client) DescribeLoadBalancersWithContext(aws.Context, *elbv2.DescribeLoadBalancersInput, ...request.Option) (*elbv2.DescribeLoadBalancersOutput, error) {
	return f.out, nil
}

func (f *fakeEC2Client) DescribeVpcEndpointServicePermissions(in *ec2.DescribeVpcEndpointServicePermissionsInput) (*ec2.DescribeVpcEndpointServicePermissionsOutput, error) {
	return f.permsOut, nil
}

func (f *fakeEC2Client) ModifyVpcEndpointServicePermissions(in *ec2.ModifyVpcEndpointServicePermissionsInput) (*ec2.ModifyVpcEndpointServicePermissionsOutput, error) {
	f.setPerms = in
	return &ec2.ModifyVpcEndpointServicePermissionsOutput{}, nil
}

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
			elbClient := &fakeElbv2Client{out: &elbv2.DescribeLoadBalancersOutput{LoadBalancers: []*elbv2.LoadBalancer{{
				LoadBalancerArn: aws.String("lb-arn"),
				State:           &elbv2.LoadBalancerState{Code: aws.String(elbv2.LoadBalancerStateEnumActive)},
			}}}}

			infra := &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.InfrastructureStatus{InfrastructureName: "management-cluster-infra-id"},
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(infra).Build()

			// Populate the test's existingAllowedPrincipals into the fakeEC2Client
			existingAllowedPrincipals := make([]*ec2.AllowedPrincipal, len(test.existingAllowedPrincipals))
			for i, p := range test.existingAllowedPrincipals {
				existingAllowedPrincipals[i] = &ec2.AllowedPrincipal{Principal: aws.String(p)}
			}

			ec2Client := &fakeEC2Client{
				createOut: &ec2.CreateVpcEndpointServiceConfigurationOutput{ServiceConfiguration: &ec2.ServiceConfiguration{ServiceName: aws.String("ep-service")}},
				permsOut: &ec2.DescribeVpcEndpointServicePermissionsOutput{
					AllowedPrincipals: existingAllowedPrincipals,
				},
			}

			r := AWSEndpointServiceReconciler{
				Client: client,
			}

			if err := r.reconcileAWSEndpointServiceStatus(context.Background(), &hyperv1.AWSEndpointService{}, &hyperv1.HostedCluster{
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
			}, ec2Client, elbClient); err != nil {
				t.Fatalf("reconcileAWSEndpointServiceStatus failed: %v", err)
			}

			if actual, expected := *ec2Client.created.TagSpecifications[0].Tags[0].Key, "kubernetes.io/cluster/management-cluster-infra-id"; actual != expected {
				t.Errorf("expected first tag key to be %s, was %s", expected, actual)
			}

			if actual, expected := *ec2Client.created.TagSpecifications[0].Tags[0].Value, "owned"; actual != expected {
				t.Errorf("expected first tags value to be %s, was %s", expected, actual)
			}

			actualToAdd := map[string]struct{}{mockControlPlaneOperatorRoleArn: {}}
			for _, arn := range ec2Client.setPerms.AddAllowedPrincipals {
				actualToAdd[*arn] = struct{}{}
			}

			for _, arn := range test.expectedPrincipalsToAdd {
				if _, ok := actualToAdd[arn]; !ok {
					t.Errorf("expected %v to be added as allowed principals, actual: %v", test.expectedPrincipalsToAdd, actualToAdd)
				}
			}

			actualToRemove := map[string]struct{}{}
			for _, arn := range ec2Client.setPerms.RemoveAllowedPrincipals {
				actualToRemove[*arn] = struct{}{}
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
		name      string
		ec2Client ec2iface.EC2API
		expected  bool
		expectErr bool
	}{
		{
			name: "successful deletion",
			ec2Client: &fakeEC2Client{
				deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
					Unsuccessful: []*ec2.UnsuccessfulItem{},
				},
			},
			expected:  true,
			expectErr: false,
		},
		{
			name: "endpoint service no longer exists",
			ec2Client: &fakeEC2Client{
				deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
					Unsuccessful: []*ec2.UnsuccessfulItem{
						{
							Error: &ec2.UnsuccessfulItemError{
								Code:    aws.String("InvalidVpcEndpointService.NotFound"),
								Message: aws.String("The VpcEndpointService Id 'vpce-svc-id' does not exist"),
							},
							ResourceId: aws.String("vpce-svc-id"),
						},
					},
				},
			},
			expected:  true,
			expectErr: false,
		},
		{
			name: "existing connections",
			ec2Client: &fakeEC2Client{
				deleteOut: &ec2.DeleteVpcEndpointServiceConfigurationsOutput{
					Unsuccessful: []*ec2.UnsuccessfulItem{
						{
							Error: &ec2.UnsuccessfulItemError{
								Code:    aws.String("ExistingVpcEndpointConnections"),
								Message: aws.String("Service has existing active VPC Endpoint connections!"),
							},
							ResourceId: aws.String("vpce-svc-id"),
						},
					},
				},
				describeOut: &ec2.DescribeVpcEndpointConnectionsOutput{
					VpcEndpointConnections: []*ec2.VpcEndpointConnection{
						{
							VpcEndpointId:    aws.String("vpce-id"),
							VpcEndpointState: aws.String("available"),
						},
					},
				},
			},
			expected:  false,
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			obj := &hyperv1.AWSEndpointService{
				Status: hyperv1.AWSEndpointServiceStatus{EndpointServiceName: "vpce-svc-id"},
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(obj).Build()

			r := AWSEndpointServiceReconciler{
				ec2Client: test.ec2Client,
				Client:    client,
			}

			ctx := log.IntoContext(context.Background(), testr.New(t))
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
