package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	capzv1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDestroyCluster(t *testing.T) {
	t.Run("When HostedCluster is nil and platform specifics provided it should call destroyPlatformSpecifics", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")

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

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		g.Expect(receivedOpts).ToNot(BeNil())
		g.Expect(receivedOpts.AzurePlatform.Cloud).To(Equal("AzurePublicCloud"))
	})

	t.Run("When kubeconfig is set it should use it for the client", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")

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

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)
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

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: cpNamespace,
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, nodePool, hcp, azureMachine, capiCluster, capiMachine, deployment, ns).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-infra",
			Log:       log.Log,
		}

		err := forceRemoveAllFinalizers(ctx, hc, opts, c)
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

		// Verify HCP finalizers are gone
		updatedHCP := &hyperv1.HostedControlPlane{}
		err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: "test-cluster"}, updatedHCP)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedHCP.Finalizers).To(BeEmpty())

		// Verify AzureMachine finalizers are gone
		updatedAzMachine := &capzv1.AzureMachine{}
		err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: "test-machine-azure-0"}, updatedAzMachine)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedAzMachine.Finalizers).To(BeEmpty())

		// Verify CAPI Cluster finalizers are gone
		updatedCluster := &capiv1.Cluster{}
		err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: "test-cluster"}, updatedCluster)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedCluster.Finalizers).To(BeEmpty())

		// Verify CAPI Machine finalizers are gone
		updatedMachine := &capiv1.Machine{}
		err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: "test-machine-0"}, updatedMachine)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedMachine.Finalizers).To(BeEmpty())

		// Verify Deployment finalizers are gone
		updatedDeploy := &appsv1.Deployment{}
		err = c.Get(ctx, types.NamespacedName{Namespace: cpNamespace, Name: "capi-provider"}, updatedDeploy)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updatedDeploy.Finalizers).To(BeEmpty())
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

		err := forceRemoveAllFinalizers(ctx, hc, opts, c)
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

func TestStripFinalizersFromList(t *testing.T) {
	t.Run("When the CRD is not installed, it should succeed without errors", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					return &apimeta.NoKindMatchError{GroupKind: capzv1.GroupVersion.WithKind("AzureMachine").GroupKind()}
				},
			}).
			Build()

		errs := stripFinalizersFromList(ctx, c, &capzv1.AzureMachineList{}, "test-ns", log.Log)
		g.Expect(errs).To(BeEmpty())
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

		err := forceRemoveAllFinalizers(ctx, hc, opts, c)
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
