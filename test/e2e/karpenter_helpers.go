//go:build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// KarpenterTestResources holds common test resources for Karpenter tests
type KarpenterTestResources struct {
	NodePool *unstructured.Unstructured
	Workload *unstructured.Unstructured
	Nodes    []corev1.Node
	Pods     []corev1.Pod
}

// createKarpenterTestNodePool creates a test NodePool with a unique name
// based on the provided base nodepool template
func createKarpenterTestNodePool(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	baseNodePool *unstructured.Unstructured,
	name string,
	replicas int,
) *unstructured.Unstructured {
	t.Helper()
	g := NewWithT(t)

	testNodePool := baseNodePool.DeepCopy()
	testNodePool.SetResourceVersion("")
	testNodePool.SetName(name)

	g.Expect(client.Create(ctx, testNodePool)).To(Succeed())
	t.Logf("Created Karpenter NodePool: %s", name)

	return testNodePool
}

// createKarpenterTestWorkload creates a test workload with a unique name
// based on the provided base workload template
func createKarpenterTestWorkload(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	baseWorkload *unstructured.Unstructured,
	name string,
	replicas int,
) *unstructured.Unstructured {
	t.Helper()
	g := NewWithT(t)

	testWorkload := baseWorkload.DeepCopy()
	testWorkload.SetResourceVersion("")
	testWorkload.SetName(name)
	testWorkload.Object["spec"].(map[string]interface{})["replicas"] = replicas

	g.Expect(client.Create(ctx, testWorkload)).To(Succeed())
	t.Logf("Created workload: %s with %d replica(s)", name, replicas)

	return testWorkload
}

// waitForKarpenterNodesReady waits for Karpenter nodes to be ready and returns them
func waitForKarpenterNodesReady(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	platform hyperv1.PlatformType,
	nodePoolName string,
	expectedCount int,
) []corev1.Node {
	t.Helper()

	nodeLabels := map[string]string{
		"karpenter.sh/nodepool": nodePoolName,
	}

	nodes := e2eutil.WaitForReadyNodesByLabels(
		t, ctx, client, platform,
		int32(expectedCount), nodeLabels,
	)
	t.Logf("Karpenter nodes ready for NodePool %s: %d", nodePoolName, len(nodes))

	return nodes
}

// cleanupKarpenterTestResources performs standard cleanup of Karpenter test resources
func cleanupKarpenterTestResources(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	workload, nodePool *unstructured.Unstructured,
) {
	t.Helper()

	if workload != nil {
		if err := client.Delete(ctx, workload); err != nil {
			t.Logf("Warning: failed to delete workload %s: %v", workload.GetName(), err)
		} else {
			t.Logf("Deleted workload: %s", workload.GetName())
		}
	}

	if nodePool != nil {
		if err := client.Delete(ctx, nodePool); err != nil {
			t.Logf("Warning: failed to delete NodePool %s: %v", nodePool.GetName(), err)
		} else {
			t.Logf("Deleted NodePool: %s", nodePool.GetName())
		}
	}
}

// waitForKarpenterNodesDeleted waits for Karpenter nodes to be deleted
func waitForKarpenterNodesDeleted(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	platform hyperv1.PlatformType,
	nodePoolName string,
) {
	t.Helper()

	nodeLabels := map[string]string{
		"karpenter.sh/nodepool": nodePoolName,
	}

	t.Logf("Waiting for Karpenter nodes to be deleted for NodePool: %s", nodePoolName)
	_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, client, platform, 0, nodeLabels)
	t.Logf("Karpenter nodes deleted for NodePool: %s", nodePoolName)
}

// createAndWaitForKarpenterResources is a convenience function that creates
// both NodePool and Workload, waits for nodes to be ready, and returns all resources
func createAndWaitForKarpenterResources(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	platform hyperv1.PlatformType,
	baseNodePool, baseWorkload *unstructured.Unstructured,
	testName string,
	replicas int,
) *KarpenterTestResources {
	t.Helper()

	nodePoolName := testName + "-nodepool"
	workloadName := testName + "-workload"

	nodePool := createKarpenterTestNodePool(t, ctx, client, baseNodePool, nodePoolName, replicas)
	workload := createKarpenterTestWorkload(t, ctx, client, baseWorkload, workloadName, replicas)

	nodes := waitForKarpenterNodesReady(t, ctx, client, platform, nodePoolName, replicas)
	pods := waitForReadyKarpenterPods(t, ctx, client, nodes, replicas)

	return &KarpenterTestResources{
		NodePool: nodePool,
		Workload: workload,
		Nodes:    nodes,
		Pods:     pods,
	}
}

// Cleanup performs cleanup for KarpenterTestResources
func (r *KarpenterTestResources) Cleanup(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
) {
	t.Helper()
	cleanupKarpenterTestResources(t, ctx, client, r.Workload, r.NodePool)
}

// WaitForDeletion waits for all resources to be deleted
func (r *KarpenterTestResources) WaitForDeletion(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	platform hyperv1.PlatformType,
) {
	t.Helper()
	if r.NodePool != nil {
		waitForKarpenterNodesDeleted(t, ctx, client, platform, r.NodePool.GetName())
	}
}

// validateBasicNodeProvisioning validates node count and labels
func validateBasicNodeProvisioning(
	t *testing.T,
	nodes []corev1.Node,
	expectedCount int,
	expectedLabels map[string]string,
) {
	t.Helper()
	g := NewWithT(t)

	g.Expect(nodes).To(HaveLen(expectedCount),
		"expected %d nodes, got %d", expectedCount, len(nodes))

	for i, node := range nodes {
		for k, v := range expectedLabels {
			g.Expect(node.Labels).To(HaveKeyWithValue(k, v),
				"node %d (%s) missing expected label %s=%s", i, node.Name, k, v)
		}
		t.Logf("Node %s has correct labels", node.Name)
	}
}
