package framework

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCluster represents a test cluster and its configuration
type TestCluster struct {
	Name         string
	Namespace    string
	HostedCluster *hyperv1.HostedCluster
	NodePools    []*hyperv1.NodePool
	CleanupFunc  func(context.Context) error
}

// ClusterManager provides utilities for managing test clusters
type ClusterManager struct {
	framework *Framework
	logger    logr.Logger
	client    client.Client
}

// NewClusterManager creates a new cluster manager
func NewClusterManager(framework *Framework) *ClusterManager {
	return &ClusterManager{
		framework: framework,
		logger:    framework.GetLogger("cluster-manager"),
		client:    framework.GetClient(),
	}
}

// CreateTestCluster creates a new test cluster with the specified configuration
func (cm *ClusterManager) CreateTestCluster(ctx context.Context, opts TestClusterOptions) (*TestCluster, error) {
	cm.logger.Info("Creating test cluster", "name", opts.Name)

	// Generate unique cluster name if not specified
	if opts.Name == "" {
		opts.Name = e2eutil.SimpleNameGenerator.GenerateName("e2e-cluster-")
	}

	// Create namespace for the cluster
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.Name,
			Labels: map[string]string{
				"test.hypershift.openshift.io/e2e-cluster": "true",
			},
		},
	}

	err := cm.client.Create(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace %s: %w", opts.Name, err)
	}

	// Create HostedCluster
	hostedCluster, err := cm.createHostedCluster(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create HostedCluster: %w", err)
	}

	// Create NodePools
	nodePools, err := cm.createNodePools(ctx, opts, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to create NodePools: %w", err)
	}

	// Create cleanup function
	cleanupFunc := func(cleanupCtx context.Context) error {
		return cm.cleanupTestCluster(cleanupCtx, opts.Name)
	}

	testCluster := &TestCluster{
		Name:          opts.Name,
		Namespace:     opts.Name,
		HostedCluster: hostedCluster,
		NodePools:     nodePools,
		CleanupFunc:   cleanupFunc,
	}

	cm.logger.Info("Test cluster created successfully", "name", opts.Name)
	return testCluster, nil
}

// WaitForClusterReady waits for the cluster to become ready
func (cm *ClusterManager) WaitForClusterReady(ctx context.Context, cluster *TestCluster) error {
	cm.logger.Info("Waiting for cluster to become ready", "name", cluster.Name)

	timeout := cm.framework.opts.ClusterCreationTimeout
	interval := 30 * time.Second

	err := wait.PollImmediate(interval, timeout, func() (bool, error) {
		// Get current HostedCluster
		current := &hyperv1.HostedCluster{}
		key := client.ObjectKey{
			Namespace: cluster.Namespace,
			Name:      cluster.Name,
		}

		if err := cm.client.Get(ctx, key, current); err != nil {
			cm.logger.Error(err, "Failed to get HostedCluster", "name", cluster.Name)
			return false, nil // Continue polling
		}

		// Check if cluster is ready
		for _, condition := range current.Status.Conditions {
			if condition.Type == string(hyperv1.HostedClusterAvailable) {
				if condition.Status == metav1.ConditionTrue {
					cm.logger.Info("Cluster is ready", "name", cluster.Name)
					return true, nil
				}
				break
			}
		}

		cm.logger.Info("Cluster not ready yet", "name", cluster.Name)
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("cluster did not become ready within timeout: %w", err)
	}

	return nil
}

// WaitForNodePoolsReady waits for all node pools to become ready
func (cm *ClusterManager) WaitForNodePoolsReady(ctx context.Context, cluster *TestCluster) error {
	cm.logger.Info("Waiting for node pools to become ready", "cluster", cluster.Name)

	timeout := cm.framework.opts.NodePoolReadyTimeout
	interval := 30 * time.Second

	for _, nodePool := range cluster.NodePools {
		cm.logger.Info("Waiting for node pool", "nodePool", nodePool.Name)

		err := wait.PollImmediate(interval, timeout, func() (bool, error) {
			// Get current NodePool
			current := &hyperv1.NodePool{}
			key := client.ObjectKey{
				Namespace: nodePool.Namespace,
				Name:      nodePool.Name,
			}

			if err := cm.client.Get(ctx, key, current); err != nil {
				cm.logger.Error(err, "Failed to get NodePool", "name", nodePool.Name)
				return false, nil // Continue polling
			}

			// Check if node pool is ready
			for _, condition := range current.Status.Conditions {
				if condition.Type == string(hyperv1.NodePoolReadyConditionType) {
					if condition.Status == corev1.ConditionTrue {
						cm.logger.Info("NodePool is ready", "name", nodePool.Name)
						return true, nil
					}
					break
				}
			}

			cm.logger.Info("NodePool not ready yet", "name", nodePool.Name)
			return false, nil
		})

		if err != nil {
			return fmt.Errorf("node pool %s did not become ready within timeout: %w", nodePool.Name, err)
		}
	}

	cm.logger.Info("All node pools are ready", "cluster", cluster.Name)
	return nil
}

// cleanupTestCluster cleans up all resources for a test cluster
func (cm *ClusterManager) cleanupTestCluster(ctx context.Context, clusterName string) error {
	cm.logger.Info("Cleaning up test cluster", "name", clusterName)

	// Skip cleanup if requested
	if cm.framework.opts.SkipTeardown {
		cm.logger.Info("Skipping cluster cleanup due to skip-teardown flag", "name", clusterName)
		return nil
	}

	// Delete the namespace (which will cascade delete all resources)
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
	}

	err := cm.client.Delete(ctx, namespace)
	if err != nil {
		cm.logger.Error(err, "Failed to delete cluster namespace", "name", clusterName)
		return err
	}

	cm.logger.Info("Test cluster cleanup completed", "name", clusterName)
	return nil
}

// TestClusterOptions holds options for creating a test cluster
type TestClusterOptions struct {
	Name              string
	Platform          hyperv1.PlatformType
	ReleaseImage      string
	NodePoolReplicas  int
	NodePoolPlatform  interface{} // Platform-specific configuration
}

// createHostedCluster creates a HostedCluster resource
func (cm *ClusterManager) createHostedCluster(ctx context.Context, opts TestClusterOptions) (*hyperv1.HostedCluster, error) {
	// This is a basic implementation - would need to be expanded with actual cluster creation logic
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Name,
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: opts.Platform,
			},
		},
	}

	// Add platform-specific configuration
	cm.configurePlatformSpec(hostedCluster, opts)

	err := cm.client.Create(ctx, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to create HostedCluster: %w", err)
	}

	return hostedCluster, nil
}

// createNodePools creates NodePool resources for the cluster
func (cm *ClusterManager) createNodePools(ctx context.Context, opts TestClusterOptions, hostedCluster *hyperv1.HostedCluster) ([]*hyperv1.NodePool, error) {
	// Create a single default NodePool
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name + "-workers",
			Namespace: opts.Name,
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: opts.Name,
			Replicas:    &[]int32{int32(opts.NodePoolReplicas)}[0],
			Platform:    hyperv1.NodePoolPlatform{
				Type: opts.Platform,
			},
		},
	}

	// Add platform-specific configuration
	cm.configureNodePoolPlatformSpec(nodePool, opts)

	err := cm.client.Create(ctx, nodePool)
	if err != nil {
		return nil, fmt.Errorf("failed to create NodePool: %w", err)
	}

	return []*hyperv1.NodePool{nodePool}, nil
}

// configurePlatformSpec configures platform-specific settings for HostedCluster
func (cm *ClusterManager) configurePlatformSpec(hostedCluster *hyperv1.HostedCluster, opts TestClusterOptions) {
	// Platform-specific configuration would go here
	// This is a placeholder implementation
	switch opts.Platform {
	case hyperv1.AWSPlatform:
		// Configure AWS-specific settings
	case hyperv1.AzurePlatform:
		// Configure Azure-specific settings
	case hyperv1.KubevirtPlatform:
		// Configure KubeVirt-specific settings
	}
}

// configureNodePoolPlatformSpec configures platform-specific settings for NodePool
func (cm *ClusterManager) configureNodePoolPlatformSpec(nodePool *hyperv1.NodePool, opts TestClusterOptions) {
	// Platform-specific configuration would go here
	// This is a placeholder implementation
	switch opts.Platform {
	case hyperv1.AWSPlatform:
		// Configure AWS-specific settings
	case hyperv1.AzurePlatform:
		// Configure Azure-specific settings
	case hyperv1.KubevirtPlatform:
		// Configure KubeVirt-specific settings
	}
}

// Ginkgo DSL helpers for common test patterns

// CreateClusterWithCleanup creates a cluster and ensures it's cleaned up after the test
func CreateClusterWithCleanup(framework *Framework, opts TestClusterOptions) *TestCluster {
	var cluster *TestCluster
	var err error

	BeforeEach(func() {
		cm := NewClusterManager(framework)
		cluster, err = cm.CreateTestCluster(context.Background(), opts)
		Expect(err).NotTo(HaveOccurred(), "Failed to create test cluster")
	})

	AfterEach(func() {
		if cluster != nil && cluster.CleanupFunc != nil {
			err := cluster.CleanupFunc(context.Background())
			if err != nil {
				framework.GetLogger("cleanup").Error(err, "Failed to cleanup cluster", "name", cluster.Name)
			}
		}
	})

	return cluster
}

// WaitForClusterAvailable waits for a cluster to become available with Ginkgo integration
func WaitForClusterAvailable(framework *Framework, cluster *TestCluster) {
	cm := NewClusterManager(framework)

	Eventually(func() error {
		return cm.WaitForClusterReady(context.Background(), cluster)
	}, framework.opts.ClusterCreationTimeout, 30*time.Second).Should(Succeed())
}

// WaitForNodePoolsAvailable waits for node pools to become available with Ginkgo integration
func WaitForNodePoolsAvailable(framework *Framework, cluster *TestCluster) {
	cm := NewClusterManager(framework)

	Eventually(func() error {
		return cm.WaitForNodePoolsReady(context.Background(), cluster)
	}, framework.opts.NodePoolReadyTimeout, 30*time.Second).Should(Succeed())
}