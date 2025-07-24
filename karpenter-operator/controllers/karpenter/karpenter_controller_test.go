package karpenter

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
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
