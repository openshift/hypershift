package karpenter

import (
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr/testr"
	"go.uber.org/mock/gomock"
)

func TestKarpenterDeletion(t *testing.T) {
	scheme := api.Scheme
	now := time.Now()

	const testNamespace = "test-namespace"

	testCases := []struct {
		name                                string
		hcp                                 *hyperv1.HostedControlPlane
		managementObjects                   []client.Object
		objects                             []client.Object
		expectedNodePools                   int
		expectedNodeClaims                  int
		eventuallyKarpenterFinalizerRemoved bool
		// expectedTerminationAnnotations maps NodeClaim name to whether it should have the termination timestamp annotation
		expectedTerminationAnnotations map[string]bool
	}{
		{
			name: "when hcp is deleted with no resources, it should remove karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
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
					Name:      "test-hcp",
					Namespace: testNamespace,
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
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
					Name:      "test-hcp",
					Namespace: testNamespace,
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
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
					Name:      "test-hcp",
					Namespace: testNamespace,
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
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
			expectedTerminationAnnotations: map[string]bool{
				"test-nodeclaim-1": true,
				"test-nodeclaim-2": true,
			},
		},
		{
			name: "when NodeClaim already has termination annotation, it should not set it again (idempotency)",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
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
			expectedTerminationAnnotations: map[string]bool{
				"test-nodeclaim-1": true, // Already has annotation, should still have it
			},
		},
		{
			name: "when NodeClaim has no deletion timestamp (orphaned), it should be explicitly deleted then get termination annotation",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
					DeletionTimestamp: &metav1.Time{
						Time: now,
					},
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
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
			expectedTerminationAnnotations: map[string]bool{
				// First reconcile explicitly deletes it (sets DeletionTimestamp),
				// second reconcile sees DeletionTimestamp and sets termination annotation
				"test-nodeclaim-orphaned": true,
			},
		},
		{
			name: "when CAPI Cluster is deleting but HCP is not, it should start node cleanup without removing karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
						"some-other-finalizer",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-id",
				},
			},
			managementObjects: []client.Object{
				&capiv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-infra-id",
						Namespace:         testNamespace,
						DeletionTimestamp: &metav1.Time{Time: now},
						Finalizers:        []string{"capi-finalizer"},
					},
				},
			},
			objects: []client.Object{
				&karpenterv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-nodepool-1",
					},
				},
			},
			expectedNodePools:                   0,
			expectedNodeClaims:                  0,
			eventuallyKarpenterFinalizerRemoved: false,
		},
		{
			name: "when CAPI Cluster is deleting with NodeClaims, it should clean up nodes without removing karpenter finalizer",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
					Finalizers: []string{
						karpenterutil.KarpenterFinalizer,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-id",
				},
			},
			managementObjects: []client.Object{
				&capiv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-infra-id",
						Namespace:         testNamespace,
						DeletionTimestamp: &metav1.Time{Time: now},
						Finalizers:        []string{"capi-finalizer"},
					},
				},
			},
			objects: []client.Object{
				&karpenterv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-nodeclaim-1",
						DeletionTimestamp: &metav1.Time{Time: now},
						Finalizers:        []string{"karpenter-finalizer"},
					},
				},
			},
			expectedNodePools:                   0,
			expectedNodeClaims:                  1,
			eventuallyKarpenterFinalizerRemoved: false,
			expectedTerminationAnnotations: map[string]bool{
				"test-nodeclaim-1": true,
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
				WithObjects(tc.managementObjects...).
				Build()

			fakeGuestClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			r := &Reconciler{
				ManagementClient: fakeManagementClient,
				GuestClient:      fakeGuestClient,
				ReleaseProvider:  mockedProvider,
				Namespace:        testNamespace,
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
				g.Expect(hcp.Finalizers).NotTo(ContainElement(karpenterutil.KarpenterFinalizer))
			} else {
				g.Expect(hcp.Finalizers).To(ContainElement(karpenterutil.KarpenterFinalizer))
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
			for nodeClaimName, shouldHaveAnnotation := range tc.expectedTerminationAnnotations {
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

func TestReconcileCRDsConcurrentAccess(t *testing.T) {
	g := NewWithT(t)

	// Snapshot the original CRD specs before any reconciliation.
	// If reconcileCRDs corrupts the globals, these will diverge.
	originalSpecs := map[string]apiextensionsv1.CustomResourceDefinitionSpec{
		crdEC2NodeClass.Name: *crdEC2NodeClass.Spec.DeepCopy(),
		crdNodePool.Name:     *crdNodePool.Spec.DeepCopy(),
		crdNodeClaim.Name:    *crdNodeClaim.Spec.DeepCopy(),
	}

	// Pre-create the CRDs in the fake client so that CreateOrUpdate's
	// internal Get() succeeds and overwrites the passed object with server state.
	existingCRDs := make([]client.Object, 0, 3)
	for _, crd := range []*apiextensionsv1.CustomResourceDefinition{
		crdEC2NodeClass,
		crdNodePool,
		crdNodeClaim,
	} {
		serverCopy := crd.DeepCopy()
		serverCopy.ResourceVersion = "999"
		serverCopy.UID = "server-uid"
		serverCopy.Generation = 42
		existingCRDs = append(existingCRDs, serverCopy)
	}

	ctx := log.IntoContext(t.Context(), testr.New(t))

	// Launch concurrent reconcilers to trigger the race.
	concurrency := 20
	var wg sync.WaitGroup
	errs := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Each goroutine gets its own fake client and reconciler to simulate
			// independent reconcile loops all sharing the same global CRD variables.
			guestClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(existingCRDs...).
				Build()
			r := &Reconciler{
				GuestClient:            guestClient,
				CreateOrUpdateProvider: upsert.New(false),
			}
			errs[index] = r.reconcileCRDs(ctx, false)
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		g.Expect(err).NotTo(HaveOccurred(), "reconcileCRDs goroutine %d failed", i)
	}

	// Verify that the global CRD variables were not corrupted by concurrent access.
	for _, crd := range []*apiextensionsv1.CustomResourceDefinition{
		crdEC2NodeClass,
		crdNodePool,
		crdNodeClaim,
	} {
		g.Expect(equality.Semantic.DeepEqual(crd.Spec, originalSpecs[crd.Name])).To(BeTrue(),
			"global CRD %q spec was corrupted by concurrent reconcileCRDs calls", crd.Name)
		g.Expect(crd.ResourceVersion).To(BeEmpty(),
			"global CRD %q had resourceVersion set by concurrent reconcileCRDs calls", crd.Name)
	}
}

func TestSumNodeClaimVCPUs(t *testing.T) {
	tests := []struct {
		name       string
		nodeClaims []karpenterv1.NodeClaim
		liveNodes  map[string]struct{}
		expected   int32
	}{
		{
			name:       "When there are no NodeClaims, it should return 0",
			nodeClaims: nil,
			liveNodes:  nil,
			expected:   0,
		},
		{
			name: "When NodeClaims have live nodes with capacity, it should sum their CPUs",
			nodeClaims: []karpenterv1.NodeClaim{
				nodeClaimWithCapacity("nc-1", "node-1", "4"),
				nodeClaimWithCapacity("nc-2", "node-2", "8"),
				nodeClaimWithCapacity("nc-3", "node-3", "16"),
			},
			liveNodes: map[string]struct{}{"node-1": {}, "node-2": {}, "node-3": {}},
			expected:  28,
		},
		{
			name: "When NodeClaims have no registered node, it should skip them",
			nodeClaims: []karpenterv1.NodeClaim{
				nodeClaimWithCapacity("nc-1", "", "4"),
				nodeClaimWithCapacity("nc-2", "", "8"),
			},
			liveNodes: nil,
			expected:  0,
		},
		{
			name: "When NodeClaims have empty capacity, it should skip them",
			nodeClaims: []karpenterv1.NodeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "nc-1"},
					Status:     karpenterv1.NodeClaimStatus{NodeName: "node-1"},
				},
			},
			liveNodes: map[string]struct{}{"node-1": {}},
			expected:  0,
		},
		{
			name: "When there is a mix of registered and unregistered NodeClaims, it should only count registered ones",
			nodeClaims: []karpenterv1.NodeClaim{
				nodeClaimWithCapacity("nc-1", "node-1", "4"),
				nodeClaimWithCapacity("nc-2", "", "8"),
				nodeClaimWithCapacity("nc-3", "node-3", "16"),
				{
					ObjectMeta: metav1.ObjectMeta{Name: "nc-4"},
					Status:     karpenterv1.NodeClaimStatus{NodeName: "node-4"},
				},
			},
			liveNodes: map[string]struct{}{"node-1": {}, "node-3": {}, "node-4": {}},
			expected:  20,
		},
		{
			name: "When NodeClaim references a node that no longer exists, it should not count it",
			nodeClaims: []karpenterv1.NodeClaim{
				nodeClaimWithCapacity("nc-1", "node-1", "4"),
				nodeClaimWithCapacity("nc-2", "node-2", "8"),
			},
			liveNodes: map[string]struct{}{"node-1": {}},
			expected:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(sumNodeClaimVCPUs(tt.nodeClaims, tt.liveNodes)).To(Equal(tt.expected))
		})
	}
}

func nodeClaimWithCapacity(name, nodeName, cpus string) karpenterv1.NodeClaim {
	nc := karpenterv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: karpenterv1.NodeClaimStatus{
			NodeName: nodeName,
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(cpus),
			},
		},
	}
	return nc
}

func TestReconcileTaintConfigMap(t *testing.T) {
	scheme := api.Scheme
	namespace := "clusters-test"

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: namespace,
		},
	}

	t.Run("When taint ConfigMap does not exist it should create it", func(t *testing.T) {
		g := NewWithT(t)
		ctx := log.IntoContext(t.Context(), testr.New(t))

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := &Reconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}

		err := r.reconcileTaintConfigMap(ctx, hcp)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{Name: karpenterutil.KarpenterTaintConfigMapName, Namespace: namespace}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cm.Data).To(HaveKey("config"))
		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cr["apiVersion"]).To(Equal("machineconfiguration.openshift.io/v1"))
		g.Expect(cr["kind"]).To(Equal("KubeletConfig"))
		metadata, ok := cr["metadata"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(metadata["name"]).To(Equal(karpenterutil.KarpenterTaintConfigMapName))
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taints).To(HaveLen(len(karpenterutil.KarpenterBaseTaints)))
		taint, ok := taints[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taint["key"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Key))
		g.Expect(taint["value"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Value))
		g.Expect(taint["effect"]).To(Equal(string(karpenterutil.KarpenterBaseTaints[0].Effect)))
	})

	t.Run("When taint ConfigMap already exists it should be idempotent", func(t *testing.T) {
		g := NewWithT(t)
		ctx := log.IntoContext(t.Context(), testr.New(t))

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterTaintConfigMapName,
				Namespace: namespace,
			},
			Data: map[string]string{"config": "old-data"},
		}
		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingCM).Build()
		r := &Reconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}

		err := r.reconcileTaintConfigMap(ctx, hcp)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{Name: karpenterutil.KarpenterTaintConfigMapName, Namespace: namespace}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		taint, ok := taints[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taint["key"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Key))
	})
}
