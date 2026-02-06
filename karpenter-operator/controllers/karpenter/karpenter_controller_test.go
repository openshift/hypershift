package karpenter

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"

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
	scheme := api.Scheme
	now := time.Now()

	testCases := []struct {
		name                                string
		hcp                                 *hyperv1.HostedControlPlane
		objects                             []client.Object
		expectedNodePools                   int
		expectedNodeClaims                  int
		eventuallyKarpenterFinalizerRemoved bool
		// expectedAnnotations maps NodeClaim name to whether it should have the termination timestamp annotation
		expectedAnnotations map[string]bool
	}{
		{
			name: "when hcp is deleted with no resources, it should remove karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterFinalizer,
						"some-other-finalizer",
					},
				},
			},
			objects:                             []client.Object{},
			expectedNodePools:                   0,
			expectedNodeClaims:                  0,
			eventuallyKarpenterFinalizerRemoved: true,
		},
		{
			name: "when hcp is deleted, it should delete karpenter NodePools and remove karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
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
			expectedNodeClaims:                  0,
			eventuallyKarpenterFinalizerRemoved: true,
		},
		{
			name: "when hcp is deleted, it should not remove karpenter finalizer if karpenter NodePools still exist",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
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
			expectedNodeClaims:                  0,
			eventuallyKarpenterFinalizerRemoved: false,
		},
		{
			name: "when hcp is deleted, it should set termination annotation on NodeClaims and not remove finalizer until they are gone",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
					DeletionTimestamp: &metav1.Time{
						Time: now,
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
						DeletionTimestamp: &metav1.Time{
							Time: now,
						},
						Finalizers: []string{"karpenter-finalizer"}, // prevents actual deletion
					},
				},
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-2",
						DeletionTimestamp: &metav1.Time{
							Time: now,
						},
						Finalizers: []string{"karpenter-finalizer"},
					},
				},
			},
			expectedNodePools:                   0,
			expectedNodeClaims:                  2,
			eventuallyKarpenterFinalizerRemoved: false,
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-1": true,
				"test-nodeclaim-2": true,
			},
		},
		{
			name: "when NodeClaim already has termination annotation, it should not set it again (idempotency)",
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
			},
			objects: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodeclaim-1",
						DeletionTimestamp: &metav1.Time{
							Time: now,
						},
						Finalizers: []string{"karpenter-finalizer"},
						Annotations: map[string]string{
							karpenterv1.NodeClaimTerminationTimestampAnnotationKey: "2024-01-01T00:00:00Z",
						},
					},
				},
			},
			expectedNodePools:                   0,
			expectedNodeClaims:                  1,
			eventuallyKarpenterFinalizerRemoved: false,
			expectedAnnotations: map[string]bool{
				"test-nodeclaim-1": true, // Already has annotation, should still have it
			},
		},
		{
			name: "when NodeClaim has no deletion timestamp (orphaned), it should be explicitly deleted then get termination annotation",
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
			},
			objects: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "test-nodeclaim-orphaned",
						Finalizers: []string{"karpenter-finalizer"},
						// No DeletionTimestamp - simulates orphaned NodeClaim
					},
				},
			},
			expectedNodePools:                   0,
			expectedNodeClaims:                  1, // Still exists due to finalizer
			eventuallyKarpenterFinalizerRemoved: false,
			expectedAnnotations: map[string]bool{
				// First reconcile explicitly deletes it (sets DeletionTimestamp),
				// second reconcile sees DeletionTimestamp and sets termination annotation
				"test-nodeclaim-orphaned": true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
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

			// verify HCP finalizers
			hcp, err := karpenterutil.GetHCP(ctx, r.ManagementClient, r.Namespace)
			g.Expect(err).NotTo(HaveOccurred())
			if tc.eventuallyKarpenterFinalizerRemoved {
				g.Expect(hcp.Finalizers).NotTo(ContainElement(karpenterFinalizer))
			} else {
				g.Expect(hcp.Finalizers).To(ContainElement(karpenterFinalizer))
			}

			// verify NodePool count
			nodePoolList := &karpenterv1.NodePoolList{}
			err = fakeGuestClient.List(ctx, nodePoolList)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(nodePoolList.Items).To(HaveLen(tc.expectedNodePools))

			// verify NodeClaim count
			nodeClaimList := &karpenterv1.NodeClaimList{}
			err = fakeGuestClient.List(ctx, nodeClaimList)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(nodeClaimList.Items).To(HaveLen(tc.expectedNodeClaims))

			// verify annotations if specified
			for nodeClaimName, shouldHaveAnnotation := range tc.expectedAnnotations {
				nodeClaim := &karpenterv1.NodeClaim{}
				err := fakeGuestClient.Get(ctx, client.ObjectKey{Name: nodeClaimName}, nodeClaim)
				g.Expect(err).NotTo(HaveOccurred())

				hasAnnotation := nodeClaim.Annotations[karpenterv1.NodeClaimTerminationTimestampAnnotationKey] != ""
				g.Expect(hasAnnotation).To(Equal(shouldHaveAnnotation),
					"NodeClaim %s: expected annotation=%v, got=%v", nodeClaimName, shouldHaveAnnotation, hasAnnotation)
			}
		})
	}
}
