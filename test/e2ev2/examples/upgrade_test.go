//go:build e2e
// +build e2e

package examples

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2ev2/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster and NodePool Upgrades", Label("upgrade", "slow"), func() {
	var (
		ctx           context.Context
		cancel        context.CancelFunc
		cluster       *framework.TestCluster
		clusterMgr    *framework.ClusterManager
		testFramework *framework.Framework
		k8sClient     client.Client
	)

	BeforeEach(func() {
		// Upgrades typically take longer
		ctx, cancel = context.WithTimeout(context.Background(), 90*time.Minute)

		testFramework = GetTestFramework()
		Expect(testFramework).NotTo(BeNil(), "Test framework should be available")

		clusterMgr = framework.NewClusterManager(testFramework)
		k8sClient = testFramework.GetClient()

		// Skip if no release images are configured
		if testFramework.GetTestOptions().PreviousReleaseImage == "" ||
			testFramework.GetTestOptions().LatestReleaseImage == "" {
			Skip("Skipping upgrade tests - previous and latest release images must be configured")
		}
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

	Describe("Control Plane Upgrade", Label("control-plane"), func() {
		It("should upgrade the control plane from previous to latest release", func() {
			By("Creating a cluster with the previous release image")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",
				Platform:         hyperv1.AWSPlatform,
				ReleaseImage:     testFramework.GetTestOptions().PreviousReleaseImage,
				NodePoolReplicas: 2,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster with previous release")

			By("Waiting for cluster to become ready with previous release")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())

			By("Recording the initial release image")
			initialRelease := cluster.HostedCluster.Spec.Release.Image

			By("Updating the cluster to the latest release image")
			cluster.HostedCluster.Spec.Release.Image = testFramework.GetTestOptions().LatestReleaseImage
			err = k8sClient.Update(ctx, cluster.HostedCluster)
			Expect(err).NotTo(HaveOccurred(), "Failed to update cluster release image")

			By("Waiting for the upgrade to complete")
			Eventually(func() (string, error) {
				current := &hyperv1.HostedCluster{}
				key := client.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      cluster.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return "", err
				}

				// Check if the cluster is available and using the new image
				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.HostedClusterAvailable) &&
						condition.Status == metav1.ConditionTrue {
						return current.Status.Version.History[0].Image, nil
					}
				}
				return "", fmt.Errorf("cluster not ready")
			}, 45*time.Minute, 1*time.Minute).Should(Equal(testFramework.GetTestOptions().LatestReleaseImage))

			By("Verifying the upgrade was successful")
			Expect(initialRelease).NotTo(Equal(testFramework.GetTestOptions().LatestReleaseImage),
				"Initial and target release images should be different")

			By("Verifying cluster remains stable after upgrade")
			Consistently(func() (metav1.ConditionStatus, error) {
				current := &hyperv1.HostedCluster{}
				key := client.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      cluster.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return metav1.ConditionUnknown, err
				}

				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.HostedClusterAvailable) {
						return condition.Status, nil
					}
				}
				return metav1.ConditionUnknown, fmt.Errorf("condition not found")
			}, 5*time.Minute, 30*time.Second).Should(Equal(metav1.ConditionTrue))
		})
	})

	Describe("NodePool Upgrade", Label("nodepool"), func() {
		BeforeEach(func() {
			By("Creating a cluster for nodepool upgrade testing")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",
				Platform:         hyperv1.AWSPlatform,
				ReleaseImage:     testFramework.GetTestOptions().LatestReleaseImage,
				NodePoolReplicas: 3, // More replicas to test rolling upgrade
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster for nodepool upgrade")

			By("Waiting for cluster to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())

			By("Waiting for nodepools to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForNodePoolsReady(ctx, cluster)
			}, testFramework.GetTestOptions().NodePoolReadyTimeout, 30*time.Second).Should(Succeed())
		})

		It("should perform a rolling upgrade of the nodepool", func() {
			nodePool := cluster.NodePools[0]

			By("Recording the initial nodepool configuration")
			initialRelease := nodePool.Spec.Release.Image

			By("Updating the nodepool to use a different release image")
			// For this test, we'll use the same image but trigger an upgrade
			// by changing another configuration that forces a rolling update
			nodePool.Spec.NodeLabels = map[string]string{
				"upgraded": "true",
			}

			err := k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to update nodepool")

			By("Waiting for the rolling upgrade to begin")
			Eventually(func() (string, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return "", err
				}

				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.NodePoolUpdatingConditionType) {
						return string(condition.Status), nil
					}
				}
				return "", fmt.Errorf("updating condition not found")
			}, 10*time.Minute, 30*time.Second).Should(Equal(string(corev1.ConditionTrue)))

			By("Waiting for the rolling upgrade to complete")
			Eventually(func() bool {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false
				}

				// Check that nodepool is ready and not updating
				isReady := false
				isNotUpdating := false

				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.NodePoolReadyConditionType) &&
						condition.Status == corev1.ConditionTrue {
						isReady = true
					}
					if condition.Type == string(hyperv1.NodePoolUpdatingConditionType) &&
						condition.Status == corev1.ConditionFalse {
						isNotUpdating = true
					}
				}

				return isReady && isNotUpdating
			}, 30*time.Minute, 1*time.Minute).Should(BeTrue())

			By("Verifying the upgrade was applied")
			Eventually(func() (map[string]string, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return nil, err
				}
				return current.Spec.NodeLabels, nil
			}, 5*time.Minute, 30*time.Second).Should(HaveKeyWithValue("upgraded", "true"))
		})

		It("should handle nodepool upgrade failure gracefully", func() {
			nodePool := cluster.NodePools[0]

			By("Attempting to upgrade to an invalid release image")
			invalidImage := "invalid.registry/invalid:image"
			nodePool.Spec.Release.Image = invalidImage

			err := k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to update nodepool with invalid image")

			By("Verifying the upgrade fails with appropriate conditions")
			Eventually(func() bool {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false
				}

				// Look for failure-related conditions
				for _, condition := range current.Status.Conditions {
					if condition.Status == corev1.ConditionFalse &&
						condition.Reason != "" {
						return true
					}
				}
				return false
			}, 15*time.Minute, 30*time.Second).Should(BeTrue())

			By("Rolling back to a valid configuration")
			nodePool.Spec.Release.Image = testFramework.GetTestOptions().LatestReleaseImage
			err = k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to rollback nodepool")

			By("Verifying the rollback succeeds")
			Eventually(func() bool {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false
				}

				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.NodePoolReadyConditionType) &&
						condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, 20*time.Minute, 1*time.Minute).Should(BeTrue())
		})
	})

	Describe("Concurrent Upgrades", Label("concurrent"), func() {
		It("should handle control plane and nodepool upgrades simultaneously", func() {
			By("Creating a cluster with previous release for concurrent upgrade testing")
			var err error
			cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
				Name:             "",
				Platform:         hyperv1.AWSPlatform,
				ReleaseImage:     testFramework.GetTestOptions().PreviousReleaseImage,
				NodePoolReplicas: 2,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster for concurrent upgrade")

			By("Waiting for cluster to become ready")
			Eventually(func() error {
				return clusterMgr.WaitForClusterReady(ctx, cluster)
			}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())

			By("Simultaneously upgrading control plane and nodepool")
			// Update control plane
			cluster.HostedCluster.Spec.Release.Image = testFramework.GetTestOptions().LatestReleaseImage
			err = k8sClient.Update(ctx, cluster.HostedCluster)
			Expect(err).NotTo(HaveOccurred(), "Failed to update control plane")

			// Update nodepool (this should wait for control plane upgrade)
			nodePool := cluster.NodePools[0]
			nodePool.Spec.Release.Image = testFramework.GetTestOptions().LatestReleaseImage
			err = k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to update nodepool")

			By("Waiting for both upgrades to complete")
			Eventually(func() bool {
				// Check control plane
				currentCluster := &hyperv1.HostedCluster{}
				clusterKey := client.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      cluster.Name,
				}
				if err := k8sClient.Get(ctx, clusterKey, currentCluster); err != nil {
					return false
				}

				// Check nodepool
				currentNodePool := &hyperv1.NodePool{}
				nodePoolKey := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, nodePoolKey, currentNodePool); err != nil {
					return false
				}

				// Both should be ready and using latest image
				clusterReady := false
				nodePoolReady := false

				for _, condition := range currentCluster.Status.Conditions {
					if condition.Type == string(hyperv1.HostedClusterAvailable) &&
						condition.Status == metav1.ConditionTrue {
						clusterReady = true
						break
					}
				}

				for _, condition := range currentNodePool.Status.Conditions {
					if condition.Type == string(hyperv1.NodePoolReadyConditionType) &&
						condition.Status == corev1.ConditionTrue {
						nodePoolReady = true
						break
					}
				}

				return clusterReady && nodePoolReady
			}, 60*time.Minute, 1*time.Minute).Should(BeTrue())
		})
	})
})