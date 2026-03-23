package aws

import (
	"context"
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
						VpcEndpointState: ec2types.StateAvailable,
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
