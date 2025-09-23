//go:build e2e
// +build e2e

package examples

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2ev2/framework"
)

var _ = Describe("Basic Cluster Lifecycle", Label("basic", "cluster", "lifecycle"), func() {
	var (
		ctx           context.Context
		cancel        context.CancelFunc
		cluster       *framework.TestCluster
		clusterMgr    *framework.ClusterManager
		testFramework *framework.Framework
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 45*time.Minute)

		// Get the test framework from the suite
		testFramework = GetTestFramework()
		Expect(testFramework).NotTo(BeNil(), "Test framework should be available")

		clusterMgr = framework.NewClusterManager(testFramework)
	})

	AfterEach(func() {
		defer cancel()

		if cluster != nil && cluster.CleanupFunc != nil {
			By("Cleaning up test cluster")
			err := cluster.CleanupFunc(ctx)
			if err != nil {
				testFramework.GetLogger("cleanup").Error(err, "Failed to cleanup cluster", "name", cluster.Name)
			}
		}
	})

	Describe("AWS Platform", Label("aws"), func() {
		BeforeEach(func() {
			if !testFramework.GetTestOptions().IsAWS() {
				Skip("Skipping AWS tests - not running on AWS platform")
			}
		})

		It("should create a basic hosted cluster and wait for it to be ready", func() {
			By("Creating a basic test cluster")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",  // Auto-generated
				Platform:         hyperv1.AWSPlatform,
				NodePoolReplicas: testFramework.GetTestOptions().NodePoolReplicas,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create test cluster")
			Expect(cluster).NotTo(BeNil())
			Expect(cluster.Name).NotTo(BeEmpty())
			Expect(cluster.Namespace).NotTo(BeEmpty())

			By("Waiting for cluster to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())

			By("Waiting for node pools to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForNodePoolsReady(ctx, cluster)
			}, testFramework.GetTestOptions().NodePoolReadyTimeout, 30*time.Second).Should(Succeed())

			By("Verifying cluster has expected node pools")
			Expect(cluster.NodePools).To(HaveLen(1), "Should have exactly one node pool")
			Expect(*cluster.NodePools[0].Spec.Replicas).To(Equal(int32(testFramework.GetTestOptions().NodePoolReplicas)))
		})

		It("should handle cluster creation with custom configuration", func() {
			By("Creating a cluster with custom configuration")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "custom-config-cluster",
				Platform:         hyperv1.AWSPlatform,
				NodePoolReplicas: 1, // Override default
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create custom cluster")

			By("Verifying custom configuration was applied")
			Expect(cluster.Name).To(ContainSubstring("custom-config"))
			Expect(*cluster.NodePools[0].Spec.Replicas).To(Equal(int32(1)))

			By("Waiting for cluster to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())
		})
	})

	Describe("Azure Platform", Label("azure"), func() {
		BeforeEach(func() {
			if !testFramework.GetTestOptions().IsAzure() {
				Skip("Skipping Azure tests - not running on Azure platform")
			}
		})

		It("should create a basic hosted cluster on Azure", func() {
			By("Creating an Azure test cluster")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",  // Auto-generated
				Platform:         hyperv1.AzurePlatform,
				NodePoolReplicas: testFramework.GetTestOptions().NodePoolReplicas,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create Azure test cluster")

			By("Waiting for Azure cluster to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())

			By("Waiting for Azure node pools to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForNodePoolsReady(ctx, cluster)
			}, testFramework.GetTestOptions().NodePoolReadyTimeout, 30*time.Second).Should(Succeed())
		})
	})

	Describe("KubeVirt Platform", Label("kubevirt"), func() {
		BeforeEach(func() {
			if !testFramework.GetTestOptions().IsKubeVirt() {
				Skip("Skipping KubeVirt tests - not running on KubeVirt platform")
			}
		})

		It("should create a basic hosted cluster on KubeVirt", func() {
			By("Creating a KubeVirt test cluster")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",  // Auto-generated
				Platform:         hyperv1.KubevirtPlatform,
				NodePoolReplicas: testFramework.GetTestOptions().NodePoolReplicas,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create KubeVirt test cluster")

			By("Waiting for KubeVirt cluster to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())

			By("Waiting for KubeVirt node pools to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForNodePoolsReady(ctx, cluster)
			}, testFramework.GetTestOptions().NodePoolReadyTimeout, 30*time.Second).Should(Succeed())
		})
	})

	Describe("Error Handling", Label("error-handling"), func() {
		It("should handle invalid cluster configuration gracefully", func() {
			By("Attempting to create a cluster with invalid configuration")
			_, err := clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",
				Platform:         "invalid-platform", // Invalid platform
				NodePoolReplicas: -1,                 // Invalid replica count
			})
			Expect(err).To(HaveOccurred(), "Should fail with invalid configuration")
		})

		It("should handle context cancellation gracefully", func() {
			By("Creating a cluster with a short timeout")
			shortCtx, shortCancel := context.WithTimeout(ctx, 1*time.Second)
			defer shortCancel()

			_, err := clusterMgr.CreateTestCluster(shortCtx, framework.TestClusterOptions{
				Name:             "timeout-test",
				Platform:         hyperv1.AWSPlatform,
				NodePoolReplicas: 1,
			})
			// This may or may not fail depending on timing, but should not panic
			if err != nil {
				Expect(err).To(MatchError(ContainSubstring("context")), "Error should be context-related")
			}
		})
	})
})

// GetTestFramework returns the global test framework instance
// This would typically be passed from the main suite
func GetTestFramework() *framework.Framework {
	// In a real implementation, this would reference the global framework
	// For now, return nil as this is a demonstration
	return nil
}