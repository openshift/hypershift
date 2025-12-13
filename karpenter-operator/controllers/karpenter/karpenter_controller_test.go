package karpenter

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/go-logr/logr/testr"
	"go.uber.org/mock/gomock"
)

func TestKarpenterDeletion(t *testing.T) {
	g := NewWithT(t)
	scheme := api.Scheme

	testCases := []struct {
		name                                string
		hcp                                 *hyperv1.HostedControlPlane
		objects                             []client.Object
		expectedNodePools                   int
		eventuallyKarpenterFinalizerRemoved bool
	}{
		{
			name: "when hcp is deleted, remove karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
					Finalizers: []string{
						karpenterFinalizer,
						"some-other-finalizer",
					},
				},
			},
			objects:                             []client.Object{},
			expectedNodePools:                   0,
			eventuallyKarpenterFinalizerRemoved: true,
		},
		{
			name: "when hcp is deleted, delete karpenter NodePools and remove karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
					Finalizers: []string{
						karpenterFinalizer,
						"some-other-finalizer",
					},
				},
			},
			objects: []client.Object{
				&karpenterv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodepool-1",
					},
				},
				&karpenterv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodepool-2",
					},
				},
			},
			expectedNodePools:                   0,
			eventuallyKarpenterFinalizerRemoved: true,
		},
		{
			name: "when hcp is deleted, should not remove karpenter finalizer if karpenter NodePools still exist",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
					Finalizers: []string{
						karpenterFinalizer,
						"some-other-finalizer",
					},
				},
			},
			objects: func() []client.Object {
				nodepool := &karpenterv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodepool-1",
					},
				}
				nodepool.SetFinalizers([]string{"some-finalizer"}) // this prevents the nodepool from being deleted
				return []client.Object{nodepool}
			}(),
			expectedNodePools:                   1,
			eventuallyKarpenterFinalizerRemoved: false,
		},
		{
			name: "when hcp is deleted, should not remove karpenter finalizer if karpenter NodeClaims still exist",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
					Finalizers: []string{
						karpenterFinalizer,
						"some-other-finalizer",
					},
				},
			},
			objects: []client.Object{
				&karpenterv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodepool-1",
					},
				},
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-1",
					},
				},
			},
			expectedNodePools:                   0,
			eventuallyKarpenterFinalizerRemoved: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			mockedProvider := releaseinfo.NewMockProvider(mockCtrl)
			mockedProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(testutils.InitReleaseImageOrDie("4.18.0"), nil).AnyTimes()
			fakeManagementClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.hcp).
				WithObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pull-secret",
					},
				}).
				Build()

			fakeGuestClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			r := &Reconciler{
				ManagementClient: fakeManagementClient,
				GuestClient:      fakeGuestClient,
				ReleaseProvider:  mockedProvider,
			}

			ctx := log.IntoContext(t.Context(), testr.New(t))

			// first reconcile should initiate deletions
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.hcp)})
			g.Expect(err).NotTo(HaveOccurred())

			// second reconcile will attempt to remove finalizers
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.hcp)})
			g.Expect(err).NotTo(HaveOccurred())

			// verify finalizers
			hcp, err := r.getHCP(ctx)
			g.Expect(err).NotTo(HaveOccurred())
			if tc.eventuallyKarpenterFinalizerRemoved {
				g.Expect(hcp.Finalizers).NotTo(ContainElement(karpenterFinalizer))
			} else {
				g.Expect(hcp.Finalizers).To(ContainElement(karpenterFinalizer))
			}

			// check if the expected amount of nodepools still exist
			nodePoolList := &karpenterv1.NodePoolList{}
			err = fakeGuestClient.List(ctx, nodePoolList)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(nodePoolList.Items).To(HaveLen(tc.expectedNodePools))
		})
	}
}

// Mock EC2 client for testing instance termination
type mockTerminatingEC2Client struct {
	ec2iface.EC2API
	terminatedInstances []*string
	terminateError      error
}

func (m *mockTerminatingEC2Client) TerminateInstancesWithContext(ctx aws.Context, input *ec2.TerminateInstancesInput, opts ...request.Option) (*ec2.TerminateInstancesOutput, error) {
	if m.terminateError != nil {
		return nil, m.terminateError
	}
	m.terminatedInstances = append(m.terminatedInstances, input.InstanceIds...)
	return &ec2.TerminateInstancesOutput{
		TerminatingInstances: []*ec2.InstanceStateChange{
			{
				InstanceId: input.InstanceIds[0],
				CurrentState: &ec2.InstanceState{
					Name: aws.String("shutting-down"),
				},
			},
		},
	}, nil
}

func TestParseEC2InstanceIDFromProviderID(t *testing.T) {
	testCases := []struct {
		name       string
		providerID string
		expected   string
	}{
		{
			name:       "valid provider ID",
			providerID: "aws:///us-east-1a/i-0123456789abcdef0",
			expected:   "i-0123456789abcdef0",
		},
		{
			name:       "valid provider ID with different region",
			providerID: "aws:///eu-west-1b/i-abcdef0123456789",
			expected:   "i-abcdef0123456789",
		},
		{
			name:       "empty provider ID",
			providerID: "",
			expected:   "",
		},
		{
			name:       "invalid provider ID - wrong scheme",
			providerID: "gcp:///us-east-1a/i-0123456789abcdef0",
			expected:   "",
		},
		{
			name:       "invalid provider ID - missing scheme",
			providerID: "us-east-1a/i-0123456789abcdef0",
			expected:   "",
		},
		{
			name:       "invalid provider ID - too few parts",
			providerID: "aws://i-0123456789abcdef0",
			expected:   "",
		},
		{
			name:       "invalid provider ID - instance ID doesn't start with i-",
			providerID: "aws:///us-east-1a/x-0123456789abcdef0",
			expected:   "",
		},
		{
			name:       "invalid provider ID - malformed",
			providerID: "invalid",
			expected:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseEC2InstanceIDFromProviderID(tc.providerID)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestNodeClaimForcefulTermination(t *testing.T) {
	g := NewWithT(t)
	scheme := api.Scheme

	now := time.Now()
	pastTimeout := now.Add(-(NodeClaimDeletionTimeout + 1*time.Minute))

	testCases := []struct {
		name                  string
		hcp                   *hyperv1.HostedControlPlane
		nodeClaims            []client.Object
		expectedTerminations  int
		expectedAnnotations   map[string]bool // NodeClaim name -> should have termination annotation
		expectedFinalizerGone bool
	}{
		{
			name: "when NodeClaim exceeds timeout, it should terminate the instance",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterFinalizer,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
				},
			},
			nodeClaims: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-1",
						DeletionTimestamp: &metav1.Time{
							Time: pastTimeout,
						},
						Finalizers: []string{"test-finalizer"},
					},
					Status: karpenterv1.NodeClaimStatus{
						NodeName:   "test-node-1",
						ProviderID: "aws:///us-east-1a/i-0123456789abcdef0",
					},
				},
			},
			expectedTerminations: 1,
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-1": true,
			},
			expectedFinalizerGone: false, // Still waiting for NodeClaim to be deleted
		},
		{
			name: "when NodeClaim has not exceeded timeout, it should not terminate",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterFinalizer,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
				},
			},
			nodeClaims: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-1",
						DeletionTimestamp: &metav1.Time{
							Time: now.Add(-1 * time.Minute), // Only 1 minute ago
						},
						Finalizers: []string{"test-finalizer"},
					},
					Status: karpenterv1.NodeClaimStatus{
						NodeName:   "test-node-1",
						ProviderID: "aws:///us-east-1a/i-0123456789abcdef0",
					},
				},
			},
			expectedTerminations: 0,
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-1": false,
			},
			expectedFinalizerGone: false,
		},
		{
			name: "when NodeClaim already has termination annotation, it should not terminate again",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterFinalizer,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
				},
			},
			nodeClaims: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-1",
						DeletionTimestamp: &metav1.Time{
							Time: pastTimeout,
						},
						Finalizers: []string{"test-finalizer"},
						Annotations: map[string]string{
							KarpenterInstanceTerminationAttemptedAnnotation: "true",
						},
					},
					Status: karpenterv1.NodeClaimStatus{
						NodeName:   "test-node-1",
						ProviderID: "aws:///us-east-1a/i-0123456789abcdef0",
					},
				},
			},
			expectedTerminations: 0,
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-1": true, // Already has annotation
			},
			expectedFinalizerGone: false,
		},
		{
			name: "when multiple NodeClaims exceed timeout, it should terminate all",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterFinalizer,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
				},
			},
			nodeClaims: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-1",
						DeletionTimestamp: &metav1.Time{
							Time: pastTimeout,
						},
						Finalizers: []string{"test-finalizer"},
					},
					Status: karpenterv1.NodeClaimStatus{
						NodeName:   "test-node-1",
						ProviderID: "aws:///us-east-1a/i-0123456789abcdef0",
					},
				},
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-2",
						DeletionTimestamp: &metav1.Time{
							Time: pastTimeout,
						},
						Finalizers: []string{"test-finalizer"},
					},
					Status: karpenterv1.NodeClaimStatus{
						NodeName:   "test-node-2",
						ProviderID: "aws:///us-east-1b/i-abcdef0123456789",
					},
				},
			},
			expectedTerminations: 2,
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-1": true,
				"test-nodeclaim-2": true,
			},
			expectedFinalizerGone: false,
		},
		{
			name: "when NodeClaim has no deletion timestamp, it should be explicitly deleted (self-healing)",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterFinalizer,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
				},
			},
			nodeClaims: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "test-nodeclaim-no-deletiontimestamp",
						Finalizers: []string{"test-finalizer"},
						// Note: No DeletionTimestamp - this simulates a stuck/orphaned NodeClaim
					},
					Status: karpenterv1.NodeClaimStatus{
						NodeName:   "test-node-orphaned",
						ProviderID: "aws:///us-east-1a/i-orphaned123456789",
					},
				},
			},
			expectedTerminations: 0, // Should not terminate since no DeletionTimestamp
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-no-deletiontimestamp": false, // No termination annotation
			},
			expectedFinalizerGone: false, // Still waiting for NodeClaim to be deleted
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			mockedProvider := releaseinfo.NewMockProvider(mockCtrl)
			mockedProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(testutils.InitReleaseImageOrDie("4.18.0"), nil).AnyTimes()

			fakeManagementClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.hcp).
				WithObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pull-secret",
					},
				}).
				Build()

			fakeGuestClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.nodeClaims...).
				Build()

			// Mock EC2 client
			mockEC2 := &mockTerminatingEC2Client{}

			r := &Reconciler{
				ManagementClient: fakeManagementClient,
				GuestClient:      fakeGuestClient,
				ReleaseProvider:  mockedProvider,
				EC2ClientFactory: func(region string) (ec2iface.EC2API, error) {
					return mockEC2, nil
				},
			}

			ctx := log.IntoContext(t.Context(), testr.New(t))

			// Reconcile - this should handle NodeClaim timeouts
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.hcp)})
			g.Expect(err).NotTo(HaveOccurred())

			// Verify terminations
			g.Expect(mockEC2.terminatedInstances).To(HaveLen(tc.expectedTerminations))

			// Verify annotations
			for nodeClaimName, shouldHaveAnnotation := range tc.expectedAnnotations {
				nodeClaim := &karpenterv1.NodeClaim{}
				err := fakeGuestClient.Get(ctx, client.ObjectKey{Name: nodeClaimName}, nodeClaim)
				g.Expect(err).NotTo(HaveOccurred())

				hasAnnotation := nodeClaim.Annotations[KarpenterInstanceTerminationAttemptedAnnotation] == "true"
				g.Expect(hasAnnotation).To(Equal(shouldHaveAnnotation))
			}
		})
	}
}
