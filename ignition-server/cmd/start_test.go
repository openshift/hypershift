package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	coordinationv1 "k8s.io/api/coordination/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestIgnitionServerLeaderElection verifies that the ignition-server manager
// is configured with leader election and successfully acquires a lease.
// This test validates:
// 1. Leader election is enabled (a lease is created)
// 2. The lease has the correct name and namespace
// 3. The lease has a holder identity
// 4. The lease has the expected duration settings
func TestIgnitionServerLeaderElection(t *testing.T) {
	g := NewWithT(t)

	// Create a cancellable context for the manager
	ctx, cancel := context.WithCancel(context.Background())

	// Start envtest
	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg).NotTo(BeNil())

	defer func() {
		// Cancel context first to stop the manager before stopping envtest
		cancel()
		err := testEnv.Stop()
		g.Expect(err).NotTo(HaveOccurred())
	}()

	// Write kubeconfig for the test environment
	kubeconfigFile, err := os.CreateTemp("", "kubeconfig-*")
	g.Expect(err).NotTo(HaveOccurred())
	defer os.Remove(kubeconfigFile.Name())

	user, err := testEnv.ControlPlane.AddUser(envtest.User{
		Name:   "test-user",
		Groups: []string{"system:masters"},
	}, nil)
	g.Expect(err).NotTo(HaveOccurred())

	kubeconfigBytes, err := user.KubeConfig()
	g.Expect(err).NotTo(HaveOccurred())

	_, err = kubeconfigFile.Write(kubeconfigBytes)
	g.Expect(err).NotTo(HaveOccurred())
	err = kubeconfigFile.Close()
	g.Expect(err).NotTo(HaveOccurred())

	// Set environment variables
	originalKubeconfig := os.Getenv("KUBECONFIG")
	originalNamespace := os.Getenv(namespaceEnvVariableName)
	defer func() {
		os.Setenv("KUBECONFIG", originalKubeconfig)
		os.Setenv(namespaceEnvVariableName, originalNamespace)
	}()

	os.Setenv("KUBECONFIG", kubeconfigFile.Name())
	os.Setenv(namespaceEnvVariableName, "default")

	// Create a temporary directory for the cache
	cacheDir, err := os.MkdirTemp("", "ignition-cache-*")
	g.Expect(err).NotTo(HaveOccurred())
	defer os.RemoveAll(cacheDir)

	// Set up the manager with leader election
	mgr, err := setUpPayloadStoreReconciler(ctx, nil, "", cacheDir, "0", "")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(mgr).NotTo(BeNil())

	// Start the manager in a goroutine
	go func() {
		err := mgr.Start(ctx)
		if err != nil && ctx.Err() == nil {
			t.Logf("Manager stopped with error: %v", err)
		}
	}()

	// Wait for the lease to be created
	k8sClient, err := client.New(cfg, client.Options{})
	g.Expect(err).NotTo(HaveOccurred())

	g.Eventually(func() error {
		lease := &coordinationv1.Lease{}
		return k8sClient.Get(ctx, client.ObjectKey{
			Namespace: "default",
			Name:      "ignition-server-leader-elect",
		}, lease)
	}, 30*time.Second, 1*time.Second).Should(Succeed())

	// Verify the lease has a holder and correct configuration
	lease := &coordinationv1.Lease{}
	err = k8sClient.Get(ctx, client.ObjectKey{
		Namespace: "default",
		Name:      "ignition-server-leader-elect",
	}, lease)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify the lease has a holder identity
	g.Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease should have a holder identity")
	g.Expect(*lease.Spec.HolderIdentity).NotTo(BeEmpty(), "Lease holder identity should not be empty")

	// Verify the lease duration is set correctly (60 seconds as configured in start.go)
	g.Expect(lease.Spec.LeaseDurationSeconds).NotTo(BeNil(), "Lease should have a duration")
	g.Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(60)), "Lease duration should be 60 seconds")
}
