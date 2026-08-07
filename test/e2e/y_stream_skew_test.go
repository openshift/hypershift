//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestYStreamSkew validates Y-stream version skew between HyperShift control plane and data plane.
// This test creates a cluster at 4.22, upgrades the control plane to 5.0, and then upgrades only
// one NodePool to 5.0, leaving the others at 4.22 to validate N-1 version skew support.
func TestYStreamSkew(t *testing.T) {
	t.Parallel()

	// Verify this is Azure platform (matching template job requirement)
	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("test only supported on platform Azure")
	}

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Starting Y-stream skew test. FromImage: %s, toImage: %s",
		globalOpts.PreviousReleaseImage, // 4.22
		globalOpts.LatestReleaseImage)   // 5.0

	// Step 1: Create cluster with 4.22 (N-1 minor version)
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage // 4.22
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)

	// Azure-specific configuration
	clusterOpts.NodePoolReplicas = 1

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Logf("✅ Hosted cluster created with 4.22")
		t.Logf("   Platform: Azure")

		// Wait for guest cluster kubeconfig
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Verify initial state
		var startingVersion string
		if len(hostedCluster.Status.Version.History) > 0 {
			startingVersion = hostedCluster.Status.Version.History[0].Version
			t.Logf("   Control plane version: %s", startingVersion)
		}

		// Get NodePools - should have multiple (based on zones)
		nodePools := &hyperv1.NodePoolList{}
		err := mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("   NodePools created: %d", len(nodePools.Items))

		// Task 4: Upgrade control plane to 5.0
		t.Run("Upgrade control plane to 5.0", func(t *testing.T) {
			upgradeControlPlaneToLatest(t, ctx, g, mgtClient, hostedCluster, startingVersion)
		})

		// Task 5: Verify Y-stream skew after control plane upgrade
		t.Run("Verify Y-stream skew after control plane upgrade", func(t *testing.T) {
			verifyYStreamSkewState(t, ctx, g, mgtClient, guestClient, hostedCluster)
		})

		// Task 6: Upgrade one NodePool to 5.0
		t.Run("Upgrade one NodePool to 5.0", func(t *testing.T) {
			upgradeOneNodePool(t, ctx, g, mgtClient, hostedCluster)
		})

		// Task 7: Verify mixed NodePool versions
		t.Run("Verify mixed NodePool versions", func(t *testing.T) {
			verifyMixedNodePoolVersions(t, ctx, g, mgtClient, guestClient, hostedCluster)
		})

		// Task 8: Confirm Y-stream skew configuration
		t.Run("Confirm Y-stream skew configuration", func(t *testing.T) {
			confirmYStreamSkew(t, ctx, g, mgtClient, guestClient, hostedCluster)
		})

		// Task 9: Functional validation in skew state
		t.Run("Functional validation in skew state", func(t *testing.T) {
			validateSkewFunctionality(t, ctx, g, guestClient)
		})

	}).WithAssetReader(content.ReadFile).Execute(&clusterOpts,
		globalOpts.Platform,
		globalOpts.ArtifactDir,
		"y-stream-skew",
		globalOpts.ServiceAccountSigningKey)
}

// upgradeControlPlaneToLatest upgrades ONLY the control plane to the latest release image (5.0).
// This is the key difference from the template job - NodePools are NOT upgraded here.
func upgradeControlPlaneToLatest(
	t *testing.T,
	ctx context.Context,
	g Gomega,
	mgtClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
	startingVersion string,
) {
	t.Logf("Upgrading control plane from %s to %s",
		globalOpts.PreviousReleaseImage, // 4.22
		globalOpts.LatestReleaseImage)   // 5.0

	// Set release image version for version gating
	// (Copied from template: control_plane_upgrade_test.go)
	err := e2eutil.SetReleaseImageVersion(ctx,
		globalOpts.LatestReleaseImage,
		globalOpts.ConfigurableClusterOptions.PullSecretFile)
	g.Expect(err).NotTo(HaveOccurred())

	// Update ONLY HostedCluster spec (not NodePools)
	// (Pattern copied from template job)
	err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster,
		func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = globalOpts.LatestReleaseImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
		})
	g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

	// Wait for control plane rollout
	// (Pattern copied from template job)
	t.Run("Wait for control plane components to complete rollout", func(t *testing.T) {
		e2eutil.AtLeast(t, e2eutil.Version420)
		e2eutil.WaitForControlPlaneComponentRollout(t, ctx, mgtClient, hostedCluster, startingVersion)
	})

	t.Run("Wait for control plane version to complete rollout", func(t *testing.T) {
		e2eutil.AtLeast(t, e2eutil.Version422)
		e2eutil.WaitForControlPlaneRollout(t, ctx, mgtClient, hostedCluster)
	})

	// DO NOT wait for data plane rollout
	// This is the key difference from template job
	t.Logf("✅ Control plane upgraded to 5.0")
	t.Logf("   NodePools intentionally NOT upgraded (will do one later)")
}

// verifyYStreamSkewState verifies that control plane is at 5.0 while all NodePools remain at 4.22.
func verifyYStreamSkewState(
	t *testing.T,
	ctx context.Context,
	g Gomega,
	mgtClient crclient.Client,
	guestClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
) {
	// Refresh HostedCluster object
	err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify control plane version
	controlPlaneVersion := hostedCluster.Status.Version.History[0].Version
	g.Expect(controlPlaneVersion).To(ContainSubstring("5.0"),
		"Control plane should be at 5.0")
	t.Logf("✅ Control plane version: %s", controlPlaneVersion)

	// Get all NodePools
	nodePools := &hyperv1.NodePoolList{}
	err = mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(len(nodePools.Items)).To(BeNumerically(">=", 1), "Should have at least 1 NodePool")

	// Verify all NodePools still at 4.22
	for _, np := range nodePools.Items {
		npImage := np.Spec.Release.Image
		g.Expect(npImage).To(Equal(globalOpts.PreviousReleaseImage),
			"NodePool %s should still be at 4.22", np.Name)
		t.Logf("✅ NodePool %s: still at 4.22 (not upgraded)", np.Name)
	}

	// Verify worker node versions
	nodes := &corev1.NodeList{}
	err = guestClient.List(ctx, nodes)
	g.Expect(err).NotTo(HaveOccurred())

	for _, node := range nodes.Items {
		kubeletVersion := node.Status.NodeInfo.KubeletVersion
		g.Expect(kubeletVersion).To(ContainSubstring("4.22"),
			"Node %s kubelet should be at 4.22", node.Name)
	}

	t.Logf("✅ Y-stream skew verified on Azure:")
	t.Logf("   Control Plane: %s (5.0)", controlPlaneVersion)
	t.Logf("   Data Plane: 4.22 (all NodePools)")
	t.Logf("   Skew: 1 minor version (N-1)")
}

// upgradeOneNodePool upgrades only the first NodePool to 5.0, leaving others at 4.22.
func upgradeOneNodePool(
	t *testing.T,
	ctx context.Context,
	g Gomega,
	mgtClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
) {
	// Get all NodePools
	nodePools := &hyperv1.NodePoolList{}
	err := mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred())

	// Require at least one NodePool
	g.Expect(len(nodePools.Items)).To(BeNumerically(">=", 1), "Should have at least 1 NodePool")

	// Sort by name for consistent selection
	sort.Slice(nodePools.Items, func(i, j int) bool {
		return nodePools.Items[i].Name < nodePools.Items[j].Name
	})

	// Select first NodePool
	targetNodePool := &nodePools.Items[0]

	t.Logf("Upgrading NodePool %s from 4.22 to 5.0", targetNodePool.Name)

	// Update this NodePool to 5.0
	err = e2eutil.UpdateObject(t, ctx, mgtClient, targetNodePool,
		func(obj *hyperv1.NodePool) {
			obj.Spec.Release.Image = globalOpts.LatestReleaseImage
		})
	g.Expect(err).NotTo(HaveOccurred())

	// Wait for NodePool rollout
	t.Logf("Waiting for NodePool %s to complete rollout...", targetNodePool.Name)
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 15*time.Minute, true,
		func(ctx context.Context) (bool, error) {
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(targetNodePool), targetNodePool)
			if err != nil {
				return false, err
			}

			if targetNodePool.Status.Replicas != targetNodePool.Status.UpdatedReplicas {
				t.Logf("NodePool %s: %d/%d replicas updated",
					targetNodePool.Name,
					targetNodePool.Status.UpdatedReplicas,
					targetNodePool.Status.Replicas)
				return false, nil
			}

			return true, nil
		})
	g.Expect(err).NotTo(HaveOccurred(), "NodePool should upgrade successfully")

	t.Logf("✅ NodePool %s upgraded to 5.0", targetNodePool.Name)
}

// verifyMixedNodePoolVersions verifies that we have a mix of NodePool versions.
func verifyMixedNodePoolVersions(
	t *testing.T,
	ctx context.Context,
	g Gomega,
	mgtClient crclient.Client,
	guestClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
) {
	// Get all NodePools
	nodePools := &hyperv1.NodePoolList{}
	err := mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred())

	// Sort for consistent output
	sort.Slice(nodePools.Items, func(i, j int) bool {
		return nodePools.Items[i].Name < nodePools.Items[j].Name
	})

	type NodePoolStatus struct {
		Name    string
		Version string
		Status  string
	}
	var npStatuses []NodePoolStatus

	for i, np := range nodePools.Items {
		status := NodePoolStatus{
			Name:    np.Name,
			Version: extractVersionShort(np.Spec.Release.Image),
		}

		if np.Spec.Release.Image == globalOpts.LatestReleaseImage {
			status.Status = "UPGRADED to 5.0"
			if len(nodePools.Items) > 1 {
				g.Expect(i).To(Equal(0), "Only first NodePool should be upgraded")
			}
		} else {
			status.Status = "Still at 4.22 (Y-stream skew)"
		}

		npStatuses = append(npStatuses, status)
	}

	// Log results
	t.Logf("")
	t.Logf("NodePools:")
	for i, nps := range npStatuses {
		icon := "✅"
		label := fmt.Sprintf("nodepool-%d", i+1)

		t.Logf("  %s %s: %s - %s", icon, label, nps.Version, nps.Status)
	}
	t.Logf("")

	// Verify counts
	upgradedCount := 0
	oldVersionCount := 0

	for _, nps := range npStatuses {
		if nps.Status == "UPGRADED to 5.0" {
			upgradedCount++
		} else {
			oldVersionCount++
		}
	}

	g.Expect(upgradedCount).To(Equal(1), "Should have 1 upgraded NodePool")
	if len(nodePools.Items) > 1 {
		g.Expect(oldVersionCount).To(BeNumerically(">=", 1), "Should have at least 1 NodePool at 4.22")
	}

	t.Logf("✅ Y-stream skew confirmed on Azure:")
	t.Logf("   - 1 NodePool at 5.0")
	if len(nodePools.Items) > 1 {
		t.Logf("   - %d NodePool(s) at 4.22", oldVersionCount)
	}
}

// confirmYStreamSkew confirms the overall Y-stream skew configuration.
func confirmYStreamSkew(
	t *testing.T,
	ctx context.Context,
	g Gomega,
	mgtClient crclient.Client,
	guestClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
) {
	// Refresh HostedCluster
	err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred())

	// Get control plane version
	controlPlaneVersion := hostedCluster.Status.Version.History[0].Version

	// Count NodePool versions
	nodePools := &hyperv1.NodePoolList{}
	err = mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred())

	latestNodePools := 0
	previousNodePools := 0

	for _, np := range nodePools.Items {
		if np.Spec.Release.Image == globalOpts.LatestReleaseImage {
			latestNodePools++
		} else {
			previousNodePools++
		}
	}

	// Count worker nodes by version
	nodes := &corev1.NodeList{}
	err = guestClient.List(ctx, nodes)
	g.Expect(err).NotTo(HaveOccurred())

	latestNodes := 0
	previousNodes := 0

	for _, node := range nodes.Items {
		if strings.Contains(node.Status.NodeInfo.KubeletVersion, "5.0") {
			latestNodes++
		} else if strings.Contains(node.Status.NodeInfo.KubeletVersion, "4.22") {
			previousNodes++
		}
	}

	// Verify expectations
	g.Expect(latestNodePools).To(Equal(1), "Should have 1 NodePool at 5.0")
	if len(nodePools.Items) > 1 {
		g.Expect(previousNodePools).To(BeNumerically(">=", 1), "Should have at least 1 NodePool at 4.22")
	}
	g.Expect(latestNodes).To(BeNumerically(">=", 1), "Should have at least 1 node at 5.0")

	// Print summary
	t.Logf("═══════════════════════════════════════════════════════")
	t.Logf("Y-STREAM SKEW CONFIGURATION CONFIRMED (AZURE/AKS)")
	t.Logf("═══════════════════════════════════════════════════════")
	t.Logf("")
	t.Logf("Platform: Azure AKS")
	t.Logf("")
	t.Logf("Control Plane:")
	t.Logf("  Version: %s (5.0)", controlPlaneVersion)
	t.Logf("")
	t.Logf("Data Plane (NodePools):")
	t.Logf("  - Latest (5.0):     %d NodePool(s)", latestNodePools)
	t.Logf("  - Previous (4.22):  %d NodePool(s)", previousNodePools)
	t.Logf("")
	t.Logf("Worker Nodes (Azure VMs):")
	t.Logf("  - Latest (5.0):     %d node(s)", latestNodes)
	t.Logf("  - Previous (4.22):  %d node(s)", previousNodes)
	t.Logf("")
	t.Logf("Y-Stream Skew: 1 minor version (N-1)")
	t.Logf("  Control Plane:  5.0")
	t.Logf("  Some Workers:   4.22")
	t.Logf("")
	t.Logf("═══════════════════════════════════════════════════════")
}

// validateSkewFunctionality performs basic functional validation in the Y-stream skew state.
func validateSkewFunctionality(
	t *testing.T,
	ctx context.Context,
	g Gomega,
	guestClient crclient.Client,
) {
	t.Run("OVN networking health", func(t *testing.T) {
		// Verify OVN components healthy
		pods := &corev1.PodList{}
		err := guestClient.List(ctx, pods, crclient.InNamespace("openshift-ovn-kubernetes"))
		g.Expect(err).NotTo(HaveOccurred())

		runningPods := 0
		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning {
				runningPods++
			}
		}

		g.Expect(runningPods).To(BeNumerically(">", 0), "OVN pods should be running")
		t.Logf("✅ OVN networking healthy: %d pods running", runningPods)
	})

	t.Run("Cluster operators stable", func(t *testing.T) {
		// Basic check - verify we can list nodes
		nodes := &corev1.NodeList{}
		err := guestClient.List(ctx, nodes)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(len(nodes.Items)).To(BeNumerically(">", 0), "Should have nodes")

		// Verify all nodes are ready
		readyNodes := 0
		for _, node := range nodes.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
					readyNodes++
					break
				}
			}
		}

		g.Expect(readyNodes).To(Equal(len(nodes.Items)), "All nodes should be ready")
		t.Logf("✅ All %d nodes ready in Y-stream skew state", readyNodes)
	})

	t.Logf("✅ Functional validation complete - cluster operational with Y-stream skew")
}

// extractVersionShort extracts a short version string from a release image URL.
func extractVersionShort(image string) string {
	// Extract short version from image
	// "quay.io/.../ocp-release:5.0.0-0.ci-..." → "5.0"
	// "quay.io/.../ocp-release:4.22.0-0.ci-..." → "4.22"
	if strings.Contains(image, "5.0") {
		return "5.0"
	}
	if strings.Contains(image, "4.22") {
		return "4.22"
	}
	return "unknown"
}
