package core

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestForceCleanupFinalizers(t *testing.T) {
	t.Run("When child resources have finalizers, it should remove them and preserve destroyFinalizer on HostedCluster", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer", destroyFinalizer},
			},
		}

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters-test-cluster",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
		}

		capiCluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters-test-cluster",
				Finalizers: []string{"cluster.cluster.x-k8s.io"},
			},
		}

		machineDeployment := &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-md",
				Namespace:  "clusters-test-cluster",
				Finalizers: []string{"machinedeployment.cluster.x-k8s.io"},
			},
		}

		machineSet := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-ms",
				Namespace:  "clusters-test-cluster",
				Finalizers: []string{"machineset.cluster.x-k8s.io"},
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-machine",
				Namespace:  "clusters-test-cluster",
				Finalizers: []string{"machine.cluster.x-k8s.io"},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, hcp, capiCluster, machineDeployment, machineSet, machine).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := forceCleanupFinalizers(ctx, hc, opts, c)
		g.Expect(err).ToNot(HaveOccurred())

		var updatedHC hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)).To(Succeed())
		g.Expect(updatedHC.Finalizers).To(ConsistOf(destroyFinalizer))

		var updatedHCP hyperv1.HostedControlPlane
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hcp), &updatedHCP)).To(Succeed())
		g.Expect(updatedHCP.Finalizers).To(BeEmpty())

		var updatedCluster capiv1.Cluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(capiCluster), &updatedCluster)).To(Succeed())
		g.Expect(updatedCluster.Finalizers).To(BeEmpty())

		var updatedMD capiv1.MachineDeployment
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(machineDeployment), &updatedMD)).To(Succeed())
		g.Expect(updatedMD.Finalizers).To(BeEmpty())

		var updatedMS capiv1.MachineSet
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(machineSet), &updatedMS)).To(Succeed())
		g.Expect(updatedMS.Finalizers).To(BeEmpty())

		var updatedMachine capiv1.Machine
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), &updatedMachine)).To(Succeed())
		g.Expect(updatedMachine.Finalizers).To(BeEmpty())
	})

	t.Run("When NodePools belong to the cluster, it should remove their finalizers", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer},
			},
		}

		ownedNodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-np",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "test-cluster",
			},
		}

		unrelatedNodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "other-np",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "other-cluster",
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, ownedNodePool, unrelatedNodePool).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := forceCleanupFinalizers(ctx, hc, opts, c)
		g.Expect(err).ToNot(HaveOccurred())

		var updatedOwned hyperv1.NodePool
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ownedNodePool), &updatedOwned)).To(Succeed())
		g.Expect(updatedOwned.Finalizers).To(BeEmpty())

		var updatedUnrelated hyperv1.NodePool
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(unrelatedNodePool), &updatedUnrelated)).To(Succeed())
		g.Expect(updatedUnrelated.Finalizers).To(ConsistOf("hypershift.openshift.io/finalizer"))
	})

	t.Run("When resources have no finalizers, it should skip them", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters",
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "clusters-test-cluster",
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, machine).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := forceCleanupFinalizers(ctx, hc, opts, c)
		g.Expect(err).ToNot(HaveOccurred())

		var updatedHC hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)).To(Succeed())
		g.Expect(updatedHC.Finalizers).To(BeEmpty())
	})

	t.Run("When the control plane namespace is empty, it should handle it gracefully", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := forceCleanupFinalizers(ctx, hc, opts, c)
		g.Expect(err).ToNot(HaveOccurred())

		var updatedHC hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)).To(Succeed())
		g.Expect(updatedHC.Finalizers).To(ConsistOf(destroyFinalizer))
	})
}

func TestRemoveFinalizersFromObject(t *testing.T) {
	t.Run("When an object has finalizers, it should remove all of them", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		obj := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test",
				Namespace:  "clusters",
				Finalizers: []string{"finalizer-1", "finalizer-2"},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(obj).
			Build()

		err := removeFinalizersFromObject(ctx, log.Log, c, obj)
		g.Expect(err).ToNot(HaveOccurred())

		var updated hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), &updated)).To(Succeed())
		g.Expect(updated.Finalizers).To(BeEmpty())
	})

	t.Run("When preserve list is specified, it should keep those finalizers", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		obj := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test",
				Namespace:  "clusters",
				Finalizers: []string{"keep-me", "remove-me", "also-keep"},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(obj).
			Build()

		err := removeFinalizersFromObject(ctx, log.Log, c, obj, "keep-me", "also-keep")
		g.Expect(err).ToNot(HaveOccurred())

		var updated hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), &updated)).To(Succeed())
		g.Expect(updated.Finalizers).To(ConsistOf("keep-me", "also-keep"))
	})

	t.Run("When the object has no finalizers, it should be a no-op", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		obj := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "clusters",
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(obj).
			Build()

		err := removeFinalizersFromObject(ctx, log.Log, c, obj)
		g.Expect(err).ToNot(HaveOccurred())

		var updated hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), &updated)).To(Succeed())
		g.Expect(updated.Finalizers).To(BeEmpty())
	})

	t.Run("When only preserved finalizers remain, it should be a no-op", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		obj := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test",
				Namespace:  "clusters",
				Finalizers: []string{destroyFinalizer},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(obj).
			Build()

		err := removeFinalizersFromObject(ctx, log.Log, c, obj, destroyFinalizer)
		g.Expect(err).ToNot(HaveOccurred())

		var updated hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), &updated)).To(Succeed())
		g.Expect(updated.Finalizers).To(ConsistOf(destroyFinalizer))
	})
}

func TestDestroyClusterForceCleanup(t *testing.T) {
	t.Run("When ForceCleanupOnTimeout is enabled and grace period expires, it should force-remove finalizers and continue cleanup", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer", destroyFinalizer},
			},
		}

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters-test-cluster",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc, hcp).
			Build()

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod:    time.Millisecond,
			ForceCleanupOnTimeout: true,
			Name:                  "test-cluster",
			Namespace:             "clusters",
			Log:                   log.Log,
		}

		err := destroyCluster(ctx, hc, opts, mockPlatformSpecifics, c)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
	})

	t.Run("When ForceCleanupOnTimeout is enabled and context is canceled, it should return error without force cleanup", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer", destroyFinalizer},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc).
			Build()

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod:    5 * time.Second,
			ForceCleanupOnTimeout: true,
			Name:                  "test-cluster",
			Namespace:             "clusters",
			Log:                   log.Log,
		}

		err := destroyCluster(ctx, hc, opts, mockPlatformSpecifics, c)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		g.Expect(platformSpecificsCalled).To(BeFalse())
	})

	t.Run("When ForceCleanupOnTimeout is disabled and grace period expires, it should return error without force cleanup", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Finalizers: []string{"hypershift.openshift.io/finalizer", destroyFinalizer},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc).
			Build()

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod:    time.Millisecond,
			ForceCleanupOnTimeout: false,
			Name:                  "test-cluster",
			Namespace:             "clusters",
			Log:                   log.Log,
		}

		err := destroyCluster(ctx, hc, opts, mockPlatformSpecifics, c)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		g.Expect(platformSpecificsCalled).To(BeFalse())
	})
}
