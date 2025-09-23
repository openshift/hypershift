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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("NodePool Management", Label("nodepool", "management"), func() {
	var (
		ctx           context.Context
		cancel        context.CancelFunc
		cluster       *framework.TestCluster
		clusterMgr    *framework.ClusterManager
		testFramework *framework.Framework
		k8sClient     client.Client
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 60*time.Minute)

		testFramework = GetTestFramework()
		Expect(testFramework).NotTo(BeNil(), "Test framework should be available")

		clusterMgr = framework.NewClusterManager(testFramework)
		k8sClient = testFramework.GetClient()

		// Create a base cluster for nodepool tests
		By("Creating a base cluster for nodepool tests")
		var err error
		cluster, err = clusterMgr.CreateTestCluster(ctx, framework.TestClusterOptions{
			Name:             "",
			Platform:         hyperv1.AWSPlatform,
			NodePoolReplicas: 1, // Start with minimal replicas
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to create base cluster")

		By("Waiting for base cluster to become ready")
		Eventually(func() error {
			return clusterMgr.WaitForClusterReady(ctx, cluster)
		}, testFramework.GetTestOptions().ClusterCreationTimeout, 30*time.Second).Should(Succeed())
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

	Describe("NodePool Scaling", Label("scaling"), func() {
		It("should scale a nodepool up and down", func() {
			nodePool := cluster.NodePools[0]
			originalReplicas := *nodePool.Spec.Replicas

			By("Scaling the nodepool up")
			newReplicas := originalReplicas + 1
			nodePool.Spec.Replicas = &newReplicas

			err := k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to update nodepool replicas")

			By("Waiting for nodepool to scale up")
			Eventually(func() (int32, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return 0, err
				}
				return current.Status.Replicas, nil
			}, 10*time.Minute, 30*time.Second).Should(Equal(newReplicas))

			By("Scaling the nodepool back down")
			nodePool.Spec.Replicas = &originalReplicas
			err = k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to scale nodepool down")

			By("Waiting for nodepool to scale down")
			Eventually(func() (int32, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return 0, err
				}
				return current.Status.Replicas, nil
			}, 10*time.Minute, 30*time.Second).Should(Equal(originalReplicas))
		})

		It("should handle scaling to zero and back", func() {
			nodePool := cluster.NodePools[0]
			originalReplicas := *nodePool.Spec.Replicas

			By("Scaling the nodepool to zero")
			zeroReplicas := int32(0)
			nodePool.Spec.Replicas = &zeroReplicas

			err := k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to scale nodepool to zero")

			By("Waiting for nodepool to scale to zero")
			Eventually(func() (int32, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return -1, err
				}
				return current.Status.Replicas, nil
			}, 10*time.Minute, 30*time.Second).Should(Equal(int32(0)))

			By("Scaling the nodepool back to original size")
			nodePool.Spec.Replicas = &originalReplicas
			err = k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to scale nodepool back up")

			By("Waiting for nodepool to scale back up")
			Eventually(func() (int32, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return -1, err
				}
				return current.Status.Replicas, nil
			}, 15*time.Minute, 30*time.Second).Should(Equal(originalReplicas))
		})
	})

	Describe("Multiple NodePools", Label("multi-nodepool"), func() {
		var additionalNodePool *hyperv1.NodePool

		AfterEach(func() {
			if additionalNodePool != nil {
				By("Cleaning up additional nodepool")
				err := k8sClient.Delete(ctx, additionalNodePool)
				if err != nil {
					testFramework.GetLogger("cleanup").Error(err, "Failed to cleanup additional nodepool")
				}
			}
		})

		It("should create and manage multiple nodepools", func() {
			By("Creating an additional nodepool")
			additionalNodePool = &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cluster.Name + "-workers-2",
					Namespace: cluster.Namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: cluster.Name,
					Replicas:    &[]int32{2}[0],
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						// Platform-specific configuration would go here
					},
				},
			}

			err := k8sClient.Create(ctx, additionalNodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to create additional nodepool")

			By("Waiting for additional nodepool to become ready")
			Eventually(func() bool {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: additionalNodePool.Namespace,
					Name:      additionalNodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false
				}

				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.NodePoolReadyConditionType) {
						return condition.Status == corev1.ConditionTrue
					}
				}
				return false
			}, 15*time.Minute, 30*time.Second).Should(BeTrue())

			By("Verifying both nodepools are functioning")
			// Check that both nodepools exist and have expected replicas
			nodePoolList := &hyperv1.NodePoolList{}
			err = k8sClient.List(ctx, nodePoolList, client.InNamespace(cluster.Namespace))
			Expect(err).NotTo(HaveOccurred())
			Expect(nodePoolList.Items).To(HaveLen(2), "Should have two nodepools")

			totalReplicas := int32(0)
			for _, np := range nodePoolList.Items {
				if np.Spec.Replicas != nil {
					totalReplicas += *np.Spec.Replicas
				}
			}
			Expect(totalReplicas).To(Equal(int32(3)), "Total replicas should be 3 (1+2)")
		})
	})

	Describe("NodePool Configuration", Label("configuration"), func() {
		It("should handle nodepool configuration updates", func() {
			nodePool := cluster.NodePools[0]

			By("Adding labels to the nodepool")
			if nodePool.Spec.NodeLabels == nil {
				nodePool.Spec.NodeLabels = make(map[string]string)
			}
			nodePool.Spec.NodeLabels["test-label"] = "test-value"

			err := k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to update nodepool labels")

			By("Verifying the configuration was applied")
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
			}, 5*time.Minute, 10*time.Second).Should(HaveKeyWithValue("test-label", "test-value"))
		})

		It("should handle nodepool taints", func() {
			nodePool := cluster.NodePools[0]

			By("Adding a taint to the nodepool")
			testTaint := corev1.Taint{
				Key:    "test-taint",
				Value:  "test-value",
				Effect: corev1.TaintEffectNoSchedule,
			}
			nodePool.Spec.Taints = append(nodePool.Spec.Taints, testTaint)

			err := k8sClient.Update(ctx, nodePool)
			Expect(err).NotTo(HaveOccurred(), "Failed to update nodepool taints")

			By("Verifying the taint was applied")
			Eventually(func() ([]corev1.Taint, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return nil, err
				}
				return current.Spec.Taints, nil
			}, 5*time.Minute, 10*time.Second).Should(ContainElement(testTaint))
		})
	})

	Describe("NodePool Conditions", Label("conditions", "status"), func() {
		It("should monitor nodepool condition transitions", func() {
			nodePool := cluster.NodePools[0]

			By("Monitoring nodepool conditions")
			Eventually(func() ([]hyperv1.NodePoolCondition, error) {
				current := &hyperv1.NodePool{}
				key := client.ObjectKey{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Name,
				}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return nil, err
				}
				return current.Status.Conditions, nil
			}, 5*time.Minute, 10*time.Second).Should(Not(BeEmpty()))

			By("Verifying Ready condition is present")
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
					if condition.Type == string(hyperv1.NodePoolReadyConditionType) {
						return condition.Status == corev1.ConditionTrue
					}
				}
				return false
			}, 15*time.Minute, 30*time.Second).Should(BeTrue())
		})
	})
})