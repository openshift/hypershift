package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/elbv2/elbv2iface"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeEC2Client struct {
	ec2iface.EC2API
	created   *ec2.CreateVpcEndpointServiceConfigurationInput
	createOut *ec2.CreateVpcEndpointServiceConfigurationOutput

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
	const mockControlPlaneOperatorRoleArn = "fakeRoleARN"

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
				controlPlaneOperatorRoleARNFn: func(ctx context.Context, hc *hyperv1.HostedCluster) (string, error) {
					return mockControlPlaneOperatorRoleArn, nil
				},
			}

			if err := r.reconcileAWSEndpointServiceStatus(context.Background(), &hyperv1.AWSEndpointService{}, &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							AdditionalAllowedPrincipals: test.additionalAllowedPrincipals,
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
