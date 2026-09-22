package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	capzv1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type fakeNamespacedResourceDiscovery struct {
	resources []*metav1.APIResourceList
	err       error
}

var testWidgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

func (d *fakeNamespacedResourceDiscovery) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	return d.resources, d.err
}

func testNamespacedResourceDiscovery() namespacedResourceDiscovery {
	verbs := []string{"list", "delete", "patch"}
	return &fakeNamespacedResourceDiscovery{resources: []*metav1.APIResourceList{
		{GroupVersion: corev1.SchemeGroupVersion.String(), APIResources: []metav1.APIResource{
			{Name: "services", Kind: "Service", Namespaced: true, Verbs: verbs},
			{Name: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true, Verbs: verbs},
		}},
		{GroupVersion: appsv1.SchemeGroupVersion.String(), APIResources: []metav1.APIResource{
			{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: verbs},
		}},
		{GroupVersion: capzv1.GroupVersion.String(), APIResources: []metav1.APIResource{
			{Name: "azuremachines", Kind: "AzureMachine", Namespaced: true, Verbs: verbs},
		}},
		{GroupVersion: capiv1.GroupVersion.String(), APIResources: []metav1.APIResource{
			{Name: "clusters", Kind: "Cluster", Namespaced: true, Verbs: verbs},
			{Name: "machines", Kind: "Machine", Namespaced: true, Verbs: verbs},
		}},
		{GroupVersion: hyperv1.GroupVersion.String(), APIResources: []metav1.APIResource{
			{Name: "hostedcontrolplanes", Kind: "HostedControlPlane", Namespaced: true, Verbs: verbs},
		}},
		{GroupVersion: testWidgetGVK.GroupVersion().String(), APIResources: []metav1.APIResource{
			{Name: "widgets", Kind: testWidgetGVK.Kind, Namespaced: true, Verbs: verbs},
		}},
	}}
}

func TestDestroyCluster(t *testing.T) {
	t.Run("When HostedCluster is nil and platform specifics provided it should call destroyPlatformSpecifics", func(t *testing.T) {
		g := NewGomegaWithT(t)

		platformSpecificsCalled := false
		var receivedOpts *DestroyOptions
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			receivedOpts = o
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
			AzurePlatform: AzurePlatformDestroyOptions{
				Cloud:    "AzurePublicCloud",
				Location: "eastus",
			},
		}

		c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		err := destroyCluster(context.Background(), c, nil, nil, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		g.Expect(receivedOpts).ToNot(BeNil())
		g.Expect(receivedOpts.AzurePlatform.Cloud).To(Equal("AzurePublicCloud"))
	})

	t.Run("When kubeconfig is set it should use it for the client", func(t *testing.T) {
		g := NewGomegaWithT(t)

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Kubeconfig:         "",
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
		}

		c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		err := destroyCluster(context.Background(), c, nil, nil, opts, mockPlatformSpecifics)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
	})

	t.Run("When destroying a hosted cluster with platform specifics, it should set the destroy finalizer and complete successfully", func(t *testing.T) {
		g := NewGomegaWithT(t)

		// Start with a realistic HC: operator's finalizer present, no destroy finalizer yet.
		// setFinalizer() inside destroyCluster will add "openshift.io/destroy-cluster".
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "clusters",
				Name:       "test-cluster",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: "test-infra",
			},
		}

		g.Expect(controllerutil.ContainsFinalizer(hc, destroyFinalizer)).To(BeFalse(),
			"destroy finalizer should not be present before destroyCluster runs")

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
		}

		// Fake client store is intentionally empty — removeFinalizer() will get
		// NotFound (the operator has already cleaned up) and return nil.
		c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		err := destroyCluster(context.Background(), c, nil, hc, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		// setFinalizer() should have added the destroy finalizer during the flow.
		// removeFinalizer() handles it (returns nil on NotFound since the fake
		// client has no HC in its store, which is the expected path when the
		// operator has already cleaned up).
		g.Expect(controllerutil.ContainsFinalizer(hc, destroyFinalizer)).To(BeTrue(),
			"destroy finalizer should have been set by setFinalizer()")
	})

	t.Run("When deleteCLISecrets fails it should log and continue", func(t *testing.T) {
		g := NewGomegaWithT(t)

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				DeleteAllOf: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
					if _, ok := obj.(*corev1.Secret); ok {
						return apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", fmt.Errorf("access denied"))
					}
					return cl.DeleteAllOf(ctx, obj, opts...)
				},
			}).
			Build()

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
		}

		err := destroyCluster(context.Background(), c, nil, nil, opts, mockPlatformSpecifics)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
	})
}

func TestForceRemoveAllFinalizers(t *testing.T) {
	t.Run("When all child resources have finalizers, it should strip them and preserve destroy finalizer on HC", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()
		cpNamespace := "clusters-test-cluster"

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer, "hypershift.openshift.io/finalizer"},
			},
		}

		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster-np1",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "test-cluster",
			},
		}

		unrelatedNodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "other-cluster-np1",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "other-cluster",
			},
		}

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  cpNamespace,
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
		}

		azureMachine := &capzv1.AzureMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-machine-azure-0",
				Namespace:  cpNamespace,
				Finalizers: []string{"azuremachine.infrastructure.cluster.x-k8s.io"},
			},
		}

		capiCluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  cpNamespace,
				Finalizers: []string{"cluster.cluster.x-k8s.io"},
			},
		}

		capiMachine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-machine-0",
				Namespace:  cpNamespace,
				Finalizers: []string{"machine.cluster.x-k8s.io"},
			},
		}

		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "capi-provider",
				Namespace:  cpNamespace,
				Finalizers: []string{"hypershift.openshift.io/component-finalizer"},
			},
		}

		widget := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": testWidgetGVK.GroupVersion().String(),
			"kind":       testWidgetGVK.Kind,
			"metadata": map[string]interface{}{
				"name":       "test-widget",
				"namespace":  cpNamespace,
				"finalizers": []interface{}{"example.com/widget-finalizer"},
			},
		}}
		widget.SetGroupVersionKind(testWidgetGVK)

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "kube-apiserver",
				Namespace:  cpNamespace,
				Finalizers: []string{"service.kubernetes.io/load-balancer-cleanup"},
			},
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "etcd-data",
				Namespace:  cpNamespace,
				Finalizers: []string{"kubernetes.io/pvc-protection"},
			},
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: cpNamespace,
			},
			Spec: corev1.NamespaceSpec{Finalizers: []corev1.FinalizerName{"kubernetes"}},
		}

		operations := []string{}
		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, nodePool, unrelatedNodePool, hcp, azureMachine, capiCluster, capiMachine, deployment, service, pvc, widget, ns).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, cl client.WithWatch, obj client.ObjectList, opts ...client.ListOption) error {
					err := cl.List(ctx, obj, opts...)
					if list, ok := obj.(*unstructured.UnstructuredList); ok && list.GroupVersionKind().GroupKind() == testWidgetGVK.GroupKind() {
						operations = append(operations, fmt.Sprintf("widget-list-%d", len(list.Items)))
					}
					return err
				},
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					err := cl.Patch(ctx, obj, patch, opts...)
					if obj.GetObjectKind().GroupVersionKind().GroupKind() == testWidgetGVK.GroupKind() {
						operations = append(operations, "widget-patch")
					}
					return err
				},
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					err := cl.Delete(ctx, obj, opts...)
					if obj.GetObjectKind().GroupVersionKind().GroupKind() == testWidgetGVK.GroupKind() {
						operations = append(operations, "widget-delete")
					}
					if namespace, ok := obj.(*corev1.Namespace); ok && namespace.Name == cpNamespace {
						operations = append(operations, "namespace-delete")
					}
					return err
				},
				SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					err := cl.SubResource(subResourceName).Update(ctx, obj, opts...)
					if subResourceName == "finalize" {
						operations = append(operations, "namespace-finalize")
					}
					return err
				},
			}).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-infra",
			Log:       log.Log,
		}

		err := forceRemoveAllFinalizers(ctx, hc, opts, c, testNamespacedResourceDiscovery())
		g.Expect(err).ToNot(HaveOccurred())

		// Verify HC retains only the destroy finalizer
		updatedHC := &hyperv1.HostedCluster{}
		err = c.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "test-cluster"}, updatedHC)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedHC.Finalizers).To(Equal([]string{destroyFinalizer}))

		// Verify NodePool finalizers are gone
		updatedNP := &hyperv1.NodePool{}
		err = c.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "test-cluster-np1"}, updatedNP)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedNP.Finalizers).To(BeEmpty())

		// Verify unrelated NodePool finalizers are preserved
		unrelatedNP := &hyperv1.NodePool{}
		err = c.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "other-cluster-np1"}, unrelatedNP)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(unrelatedNP.Finalizers).To(Equal([]string{"hypershift.openshift.io/finalizer"}))

		// Verify every namespaced object, including Service and PVC objects that
		// were absent from the old fixed allowlist, is removed before namespace
		// finalization.
		for _, expected := range []struct {
			name   string
			object client.Object
		}{
			{name: "test-cluster", object: &hyperv1.HostedControlPlane{}},
			{name: "test-machine-azure-0", object: &capzv1.AzureMachine{}},
			{name: "test-cluster", object: &capiv1.Cluster{}},
			{name: "test-machine-0", object: &capiv1.Machine{}},
			{name: "capi-provider", object: &appsv1.Deployment{}},
			{name: "kube-apiserver", object: &corev1.Service{}},
			{name: "etcd-data", object: &corev1.PersistentVolumeClaim{}},
		} {
			err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: expected.name}, expected.object)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected %T %s to be deleted", expected.object, expected.name)
		}
		updatedWidget := &unstructured.Unstructured{}
		updatedWidget.SetGroupVersionKind(testWidgetGVK)
		err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: "test-widget"}, updatedWidget)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected arbitrary unstructured Widget to be deleted")

		indexOf := func(operation string) int {
			for i, recorded := range operations {
				if recorded == operation {
					return i
				}
			}
			return -1
		}
		finalizeIndex := indexOf("namespace-finalize")
		namespaceDeleteIndex := indexOf("namespace-delete")
		g.Expect(indexOf("widget-patch")).To(BeNumerically("<", finalizeIndex))
		g.Expect(indexOf("widget-delete")).To(BeNumerically("<", finalizeIndex))
		g.Expect(indexOf("widget-list-0")).To(BeNumerically("<", finalizeIndex))
		g.Expect(indexOf("widget-patch")).To(BeNumerically("<", namespaceDeleteIndex))
		g.Expect(indexOf("widget-delete")).To(BeNumerically("<", namespaceDeleteIndex))
		g.Expect(indexOf("widget-list-0")).To(BeNumerically("<", namespaceDeleteIndex))
	})

	t.Run("When the control plane namespace is empty, it should succeed without errors", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "empty-cluster",
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc).
			Build()

		opts := &DestroyOptions{
			Name:      "empty-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := forceRemoveAllFinalizers(ctx, hc, opts, c, testNamespacedResourceDiscovery())
		g.Expect(err).ToNot(HaveOccurred())

		updatedHC := &hyperv1.HostedCluster{}
		err = c.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "empty-cluster"}, updatedHC)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedHC.Finalizers).To(Equal([]string{destroyFinalizer}))
	})
}

func TestStripFinalizers(t *testing.T) {
	t.Run("When an object has finalizers, it should remove all of them", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test",
				Namespace:  "test-ns",
				Finalizers: []string{"fin1", "fin2", "fin3"},
			},
		}

		c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hcp).Build()

		err := stripFinalizers(ctx, c, hcp, log.Log)
		g.Expect(err).ToNot(HaveOccurred())

		updated := &hyperv1.HostedControlPlane{}
		err = c.Get(ctx, client.ObjectKeyFromObject(hcp), updated)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updated.Finalizers).To(BeEmpty())
	})

	t.Run("When an object has no finalizers, it should succeed without errors", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test-ns",
			},
		}

		c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hcp).Build()

		err := stripFinalizers(ctx, c, hcp, log.Log)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When the patch fails, it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test",
				Namespace:  "test-ns",
				Finalizers: []string{"fin1"},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hcp).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(_ context.Context, _ client.WithWatch, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
					return fmt.Errorf("API server unavailable")
				},
			}).
			Build()

		err := stripFinalizers(ctx, c, hcp, log.Log)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("API server unavailable"))
	})
}

func TestCleanupNamespacedResources(t *testing.T) {
	t.Run("When discovery is unavailable, it should fail closed before namespace finalization", func(t *testing.T) {
		g := NewGomegaWithT(t)
		c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

		errs := cleanupNamespacedResources(context.Background(), c, &fakeNamespacedResourceDiscovery{err: fmt.Errorf("discovery unavailable")}, "test-ns", log.Log)
		g.Expect(errs).To(HaveLen(1))
		g.Expect(errs[0].Error()).To(ContainSubstring("discovery unavailable"))
	})
}

func TestForceRemoveAllFinalizersDiscoveryError(t *testing.T) {
	t.Run("When discovery fails, it should not finalize the control plane namespace", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()
		clusterName := "discovery-error-cluster"
		cpNamespace := manifests.HostedControlPlaneNamespace("clusters", clusterName)
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer, "hypershift.openshift.io/finalizer"},
			},
		}
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: cpNamespace},
			Spec:       corev1.NamespaceSpec{Finalizers: []corev1.FinalizerName{"kubernetes"}},
		}
		operations := []string{}
		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, ns).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					if subResourceName == "finalize" {
						operations = append(operations, "namespace-finalize")
					}
					return cl.SubResource(subResourceName).Update(ctx, obj, opts...)
				},
			}).
			Build()

		opts := &DestroyOptions{Name: clusterName, Namespace: "clusters", Log: log.Log}
		err := forceRemoveAllFinalizers(ctx, hc, opts, c, &fakeNamespacedResourceDiscovery{err: fmt.Errorf("discovery unavailable")})
		g.Expect(err).To(HaveOccurred())
		g.Expect(operations).NotTo(ContainElement("namespace-finalize"))

		updatedNamespace := &corev1.Namespace{}
		err = c.Get(ctx, types.NamespacedName{Name: cpNamespace}, updatedNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedNamespace.Spec.Finalizers).To(Equal([]corev1.FinalizerName{"kubernetes"}))
	})
}

func TestStripNodePoolFinalizers(t *testing.T) {
	t.Run("When NodePools belong to different HostedClusters, it should only strip finalizers from matching NodePools", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster-np1",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "test-cluster",
			},
		}
		unrelatedNodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "other-cluster-np1",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "other-cluster",
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(nodePool, unrelatedNodePool).
			Build()

		errs := stripNodePoolFinalizers(ctx, c, "clusters", "test-cluster", log.Log)
		g.Expect(errs).To(BeEmpty())

		updatedNodePool := &hyperv1.NodePool{}
		err := c.Get(ctx, client.ObjectKeyFromObject(nodePool), updatedNodePool)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedNodePool.Finalizers).To(BeEmpty())

		updatedUnrelatedNodePool := &hyperv1.NodePool{}
		err = c.Get(ctx, client.ObjectKeyFromObject(unrelatedNodePool), updatedUnrelatedNodePool)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedUnrelatedNodePool.Finalizers).To(Equal([]string{"hypershift.openshift.io/finalizer"}))
	})
}

func TestForceRemoveAllFinalizersErrors(t *testing.T) {
	t.Run("When client operations fail, it should return an aggregated error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer, "hypershift.openshift.io/finalizer"},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(_ context.Context, _ client.WithWatch, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
					return fmt.Errorf("API server unavailable")
				},
			}).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := forceRemoveAllFinalizers(ctx, hc, opts, c, testNamespacedResourceDiscovery())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("force removal encountered"))
		g.Expect(err.Error()).To(ContainSubstring("API server unavailable"))
	})
}

func TestGetCluster(t *testing.T) {
	t.Run("When kubeconfig is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "")

		opts := &DestroyOptions{
			Kubeconfig: "/nonexistent/kubeconfig",
			Name:       "test-cluster",
			Namespace:  "clusters",
		}

		_, err := GetCluster(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
	})
}
