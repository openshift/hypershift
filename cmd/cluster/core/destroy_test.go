package core

import (
	"context"
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
	t.Run("should remove finalizers from HostedCluster and all child resources", func(t *testing.T) {
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

		forceCleanupFinalizers(ctx, hc, opts, c)

		var updatedHC hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)).To(Succeed())
		g.Expect(updatedHC.Finalizers).To(BeEmpty())

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

	t.Run("should skip resources without finalizers", func(t *testing.T) {
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

		forceCleanupFinalizers(ctx, hc, opts, c)

		var updatedHC hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)).To(Succeed())
		g.Expect(updatedHC.Finalizers).To(BeEmpty())
	})

	t.Run("should handle empty control plane namespace gracefully", func(t *testing.T) {
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

		forceCleanupFinalizers(ctx, hc, opts, c)

		var updatedHC hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)).To(Succeed())
		g.Expect(updatedHC.Finalizers).To(BeEmpty())
	})
}

func TestRemoveFinalizersFromObject(t *testing.T) {
	t.Run("should remove all finalizers from object", func(t *testing.T) {
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

		removeFinalizersFromObject(ctx, log.Log, c, obj)

		var updated hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), &updated)).To(Succeed())
		g.Expect(updated.Finalizers).To(BeEmpty())
	})

	t.Run("should be a no-op when object has no finalizers", func(t *testing.T) {
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

		removeFinalizersFromObject(ctx, log.Log, c, obj)

		var updated hyperv1.HostedCluster
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), &updated)).To(Succeed())
		g.Expect(updated.Finalizers).To(BeEmpty())
	})
}
