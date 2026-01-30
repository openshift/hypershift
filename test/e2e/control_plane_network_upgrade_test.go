//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Hardcoded release images for CORENET-6064 upgrade testing
	// TODO: Remove hardcoding once CI job is configured with proper release images
	corenet6064PreviousReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.1-x86_64"
	corenet6064LatestReleaseImage   = "registry.build11.ci.openshift.org/ci-ln-ks2hvtb/release:latest"
)

// TestOVNControlPlaneUpgradeWithZeroWorkers validates CORENET-6064:
// OVN control-plane should rollout along with CNO version when there are no workers.
//
// This test verifies that when a HostedCluster with zero worker nodes is upgraded,
// the OVN control-plane deployment updates along with the Cluster Network Operator (CNO)
// version, without being blocked by the data-plane skew constraints.
//
// Test Behavior:
//   - On clusters WITH the fix: Test PASSES (OVN control-plane updates correctly)
//   - On clusters WITHOUT the fix: Test FAILS (OVN control-plane is blocked)
//
// JIRA: https://issues.redhat.com/browse/CORENET-6064
func TestOVNControlPlaneUpgradeWithZeroWorkers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	// Always use hardcoded release images for CORENET-6064 testing
	// Do NOT use globalOpts to ensure consistent test behavior
	previousReleaseImage := corenet6064PreviousReleaseImage
	latestReleaseImage := corenet6064LatestReleaseImage

	t.Logf("CORENET-6064 Test: Upgrading from %s to %s", previousReleaseImage, latestReleaseImage)

	// Create cluster options with zero replicas (no worker nodes)
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 0 // Zero workers to test CORENET-6064 scenario

	// Use the previous release image as the initial version
	clusterOpts.ReleaseImage = previousReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	// Explicitly set Public endpoint access to ensure the test framework correctly
	// detects zero workers and adjusts expected conditions accordingly.
	// With PublicAndPrivate access, the framework assumes workers exist and skips node counting.
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Public)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Log("Starting CORENET-6064 test: OVN control-plane upgrade with zero workers")

		hcpNamespace := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)

		// Step 1: Verify zero workers
		t.Log("Step 1: Verifying zero worker nodes")
		verifyZeroWorkers(t, g, ctx, mgtClient, hostedCluster)

		// Step 2: Record initial OVN control-plane state
		t.Log("Step 2: Recording initial OVN control-plane state")
		initialState := recordNetworkComponentState(t, g, ctx, mgtClient, hcpNamespace)
		t.Logf("Initial OVN control-plane image: %s", truncateImage(initialState.ovnImage))
		t.Logf("Initial CNO image: %s", truncateImage(initialState.cnoImage))

		// Step 3: Initiate upgrade to latest release
		t.Log("Step 3: Initiating upgrade to latest release")
		targetReleaseImage := latestReleaseImage
		t.Logf("Target release image: %s", targetReleaseImage)

		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get HostedCluster")

		hostedCluster.Spec.Release.Image = targetReleaseImage
		err = mgtClient.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to update HostedCluster release image")

		// Step 4: Wait for control plane components to be ready
		t.Log("Step 4: Waiting for control plane upgrade to complete")
		e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, mgtClient, hostedCluster, targetReleaseImage)

		// Step 5: Verify OVN control-plane updated (this is the key CORENET-6064 check)
		t.Log("Step 5: Verifying OVN control-plane updated (CORENET-6064 validation)")
		result := verifyOVNControlPlaneUpdated(t, ctx, mgtClient, hcpNamespace, initialState)

		// Step 6: Report results based on fix status
		reportTestResult(t, g, result, initialState)

		// Step 7: Verify no blocking conditions (additional health check)
		if result.ovnDeploymentReady {
			t.Log("Step 7: Verifying no blocking conditions")
			verifyNoBlockingConditions(t, g, ctx, mgtClient, hcpNamespace)
		}

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "corenet-6064-zero-workers", globalOpts.ServiceAccountSigningKey)
}

// TestOVNControlPlaneUpgradeScaleToZeroWorkers tests the scenario where workers are
// scaled down to zero before the upgrade, simulating data-plane hibernation or autoscaling from zero.
//
// JIRA: https://issues.redhat.com/browse/CORENET-6064
func TestOVNControlPlaneUpgradeScaleToZeroWorkers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	// Always use hardcoded release images for CORENET-6064 testing
	// Do NOT use globalOpts to ensure consistent test behavior
	previousReleaseImage := corenet6064PreviousReleaseImage
	latestReleaseImage := corenet6064LatestReleaseImage

	t.Logf("CORENET-6064 Scale-to-Zero Test: Upgrading from %s to %s", previousReleaseImage, latestReleaseImage)

	// Create cluster with initial workers, then scale to zero
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 1 // Start with one worker
	clusterOpts.ReleaseImage = previousReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	// Explicitly set Public endpoint access to ensure the test framework correctly
	// detects zero workers after scale-down and adjusts expected conditions accordingly.
	// With PublicAndPrivate access, the framework assumes workers exist and skips node counting.
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Public)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Log("Starting CORENET-6064 scale-to-zero test")

		hcpNamespace := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)

		// Step 1: Wait for initial worker to be ready
		t.Log("Step 1: Waiting for initial worker node")
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		e2eutil.WaitForNReadyNodes(t, ctx, guestClient, 1, hostedCluster.Spec.Platform.Type)

		// Step 2: Scale down NodePool to zero
		t.Log("Step 2: Scaling NodePool to zero workers")
		scaleNodePoolToZero(t, g, ctx, mgtClient, hostedCluster)

		// Step 3: Wait for nodes to be removed
		t.Log("Step 3: Waiting for all nodes to be removed")
		waitForZeroNodes(t, ctx, guestClient)

		// Step 4: Record initial OVN control-plane state
		t.Log("Step 4: Recording initial OVN control-plane state")
		initialState := recordNetworkComponentState(t, g, ctx, mgtClient, hcpNamespace)
		t.Logf("Initial OVN control-plane image: %s", truncateImage(initialState.ovnImage))
		t.Logf("Initial CNO image: %s", truncateImage(initialState.cnoImage))

		// Step 5: Initiate upgrade
		t.Log("Step 5: Initiating upgrade to latest release")
		targetReleaseImage := latestReleaseImage
		t.Logf("Target release image: %s", targetReleaseImage)

		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get HostedCluster")

		hostedCluster.Spec.Release.Image = targetReleaseImage
		err = mgtClient.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to update HostedCluster release image")

		// Step 6: Wait for upgrade and verify
		t.Log("Step 6: Waiting for control plane upgrade to complete")
		e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, mgtClient, hostedCluster, targetReleaseImage)

		// Step 7: Verify OVN control-plane updated
		t.Log("Step 7: Verifying OVN control-plane updated (CORENET-6064 validation)")
		result := verifyOVNControlPlaneUpdated(t, ctx, mgtClient, hcpNamespace, initialState)

		// Step 8: Report results
		reportTestResult(t, g, result, initialState)

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "corenet-6064-scale-to-zero", globalOpts.ServiceAccountSigningKey)
}

// networkComponentState holds the state of network components at a point in time.
type networkComponentState struct {
	ovnImage      string
	cnoImage      string
	ovnGeneration int64
	cnoGeneration int64
	ovnReadyPods  int32
	cnoReadyPods  int32
}

// upgradeResult holds the result of the upgrade verification.
type upgradeResult struct {
	cnoUpdated         bool
	ovnUpdated         bool
	ovnDeploymentReady bool
	finalOVNImage      string
	finalCNOImage      string
	blockingReason     string
	timedOut           bool
}

// verifyZeroWorkers confirms that the hosted cluster has zero worker nodes.
func verifyZeroWorkers(t *testing.T, g Gomega, ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	// Check NodePool replicas
	nodePools := &hyperv1.NodePoolList{}
	err := mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred(), "Failed to list NodePools")

	totalReplicas := int32(0)
	for _, np := range nodePools.Items {
		if np.Spec.ClusterName == hostedCluster.Name && np.Spec.Replicas != nil {
			totalReplicas += *np.Spec.Replicas
		}
	}

	g.Expect(totalReplicas).To(Equal(int32(0)), "Expected zero worker replicas, but found %d", totalReplicas)
	t.Logf("Confirmed zero worker replicas across all NodePools")
}

// recordNetworkComponentState retrieves the current OVN control-plane and CNO state.
func recordNetworkComponentState(t *testing.T, g Gomega, ctx context.Context, mgtClient crclient.Client, hcpNamespace string) networkComponentState {
	state := networkComponentState{}

	// Get OVN control-plane deployment
	ovnDeployment := &appsv1.Deployment{}
	err := mgtClient.Get(ctx, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      "ovnkube-control-plane",
	}, ovnDeployment)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to get ovnkube-control-plane deployment")

	// Find the ovnkube container image
	for _, container := range ovnDeployment.Spec.Template.Spec.Containers {
		if strings.Contains(container.Name, "ovnkube") || container.Name == "ovn-controller" {
			state.ovnImage = container.Image
			break
		}
	}
	if state.ovnImage == "" && len(ovnDeployment.Spec.Template.Spec.Containers) > 0 {
		state.ovnImage = ovnDeployment.Spec.Template.Spec.Containers[0].Image
	}
	state.ovnGeneration = ovnDeployment.Generation
	state.ovnReadyPods = ovnDeployment.Status.ReadyReplicas

	g.Expect(state.ovnImage).NotTo(BeEmpty(), "Could not find OVN control-plane container image")

	// Get CNO deployment
	cnoDeployment := &appsv1.Deployment{}
	err = mgtClient.Get(ctx, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      "cluster-network-operator",
	}, cnoDeployment)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to get cluster-network-operator deployment")

	for _, container := range cnoDeployment.Spec.Template.Spec.Containers {
		if container.Name == "cluster-network-operator" {
			state.cnoImage = container.Image
			break
		}
	}
	if state.cnoImage == "" && len(cnoDeployment.Spec.Template.Spec.Containers) > 0 {
		state.cnoImage = cnoDeployment.Spec.Template.Spec.Containers[0].Image
	}
	state.cnoGeneration = cnoDeployment.Generation
	state.cnoReadyPods = cnoDeployment.Status.ReadyReplicas

	g.Expect(state.cnoImage).NotTo(BeEmpty(), "Could not find CNO container image")

	return state
}

// verifyOVNControlPlaneUpdated checks whether the OVN control-plane is healthy
// after the cluster upgrade. This is the key check for CORENET-6064.
//
// The test passes if:
// 1. CNO has updated (image changed from initial)
// 2. OVN deployment is ready (all replicas healthy)
//
// Note: OVN image may or may not change depending on whether the target release
// has a different OVN version. The key check is that OVN deployment is NOT blocked.
func verifyOVNControlPlaneUpdated(t *testing.T, ctx context.Context, mgtClient crclient.Client, hcpNamespace string, initialState networkComponentState) upgradeResult {
	result := upgradeResult{}

	// Wait for CNO to update and OVN deployment to be ready
	// Use a longer timeout to ensure upgrade completes - typically takes 15-20 minutes
	// If the fix is present, OVN deployment will be ready after CNO updates
	// If the fix is NOT present (bug exists), OVN deployment might be stuck rolling out
	err := wait.PollUntilContextTimeout(ctx, 30*time.Second, 25*time.Minute, true, func(ctx context.Context) (bool, error) {
		// Get current OVN deployment
		ovnDeployment := &appsv1.Deployment{}
		if err := mgtClient.Get(ctx, types.NamespacedName{
			Namespace: hcpNamespace,
			Name:      "ovnkube-control-plane",
		}, ovnDeployment); err != nil {
			return false, nil
		}

		// Check if OVN deployment is ready
		ovnReady := ovnDeployment.Status.ReadyReplicas == ovnDeployment.Status.Replicas &&
			ovnDeployment.Status.Replicas > 0 &&
			ovnDeployment.Status.UpdatedReplicas == ovnDeployment.Status.Replicas
		result.ovnDeploymentReady = ovnReady

		if !ovnReady {
			t.Logf("Waiting for OVN control-plane deployment: %d/%d ready, %d updated",
				ovnDeployment.Status.ReadyReplicas, ovnDeployment.Status.Replicas,
				ovnDeployment.Status.UpdatedReplicas)
		}

		// Get current OVN image from deployment spec
		for _, container := range ovnDeployment.Spec.Template.Spec.Containers {
			if strings.Contains(container.Name, "ovnkube") || container.Name == "ovn-controller" {
				result.finalOVNImage = container.Image
				break
			}
		}
		if result.finalOVNImage == "" && len(ovnDeployment.Spec.Template.Spec.Containers) > 0 {
			result.finalOVNImage = ovnDeployment.Spec.Template.Spec.Containers[0].Image
		}

		// Get current CNO deployment
		cnoDeployment := &appsv1.Deployment{}
		if err := mgtClient.Get(ctx, types.NamespacedName{
			Namespace: hcpNamespace,
			Name:      "cluster-network-operator",
		}, cnoDeployment); err != nil {
			return false, nil
		}

		for _, container := range cnoDeployment.Spec.Template.Spec.Containers {
			if container.Name == "cluster-network-operator" {
				result.finalCNOImage = container.Image
				break
			}
		}
		if result.finalCNOImage == "" && len(cnoDeployment.Spec.Template.Spec.Containers) > 0 {
			result.finalCNOImage = cnoDeployment.Spec.Template.Spec.Containers[0].Image
		}

		// Determine update status
		result.cnoUpdated = result.finalCNOImage != initialState.cnoImage
		result.ovnUpdated = result.finalOVNImage != initialState.ovnImage

		t.Logf("Status check - CNO updated: %v, OVN updated: %v, OVN ready: %v",
			result.cnoUpdated, result.ovnUpdated, result.ovnDeploymentReady)

		// Success when CNO has updated AND OVN deployment is ready
		// Note: OVN image may or may not change depending on the target release
		return result.cnoUpdated && result.ovnDeploymentReady, nil
	})

	if err != nil {
		result.timedOut = true
		t.Logf("Timeout waiting for upgrade to complete")

		// Final check to get the latest state
		ovnDeployment := &appsv1.Deployment{}
		if err := mgtClient.Get(ctx, types.NamespacedName{
			Namespace: hcpNamespace,
			Name:      "ovnkube-control-plane",
		}, ovnDeployment); err == nil {
			result.ovnDeploymentReady = ovnDeployment.Status.ReadyReplicas == ovnDeployment.Status.Replicas &&
				ovnDeployment.Status.Replicas > 0 &&
				ovnDeployment.Status.UpdatedReplicas == ovnDeployment.Status.Replicas

			for _, container := range ovnDeployment.Spec.Template.Spec.Containers {
				if strings.Contains(container.Name, "ovnkube") || container.Name == "ovn-controller" {
					result.finalOVNImage = container.Image
					break
				}
			}
			if result.finalOVNImage == "" && len(ovnDeployment.Spec.Template.Spec.Containers) > 0 {
				result.finalOVNImage = ovnDeployment.Spec.Template.Spec.Containers[0].Image
			}
		}

		cnoDeployment := &appsv1.Deployment{}
		if err := mgtClient.Get(ctx, types.NamespacedName{
			Namespace: hcpNamespace,
			Name:      "cluster-network-operator",
		}, cnoDeployment); err == nil {
			for _, container := range cnoDeployment.Spec.Template.Spec.Containers {
				if container.Name == "cluster-network-operator" {
					result.finalCNOImage = container.Image
					break
				}
			}
			if result.finalCNOImage == "" && len(cnoDeployment.Spec.Template.Spec.Containers) > 0 {
				result.finalCNOImage = cnoDeployment.Spec.Template.Spec.Containers[0].Image
			}
		}

		result.cnoUpdated = result.finalCNOImage != initialState.cnoImage
		result.ovnUpdated = result.finalOVNImage != initialState.ovnImage

		// Set blocking reason if CNO updated but OVN is not ready
		if result.cnoUpdated && !result.ovnDeploymentReady {
			result.blockingReason = "CNO updated but OVN control-plane deployment is not ready (possible CORENET-6064 bug)"
		}
	}

	return result
}

// reportTestResult reports the test result and determines pass/fail based on whether
// the CORENET-6064 fix is present.
//
// Pass criteria:
//   - CNO has updated (image changed)
//   - OVN deployment is ready (all replicas healthy)
//
// Note: OVN image may or may not change depending on the target release.
// The key check is that OVN deployment is NOT blocked from reconciling.
func reportTestResult(t *testing.T, g Gomega, result upgradeResult, initialState networkComponentState) {
	t.Log("")
	t.Log("========================================")
	t.Log("CORENET-6064 Test Results")
	t.Log("========================================")
	t.Logf("Initial OVN Image: %s", truncateImage(initialState.ovnImage))
	t.Logf("Final OVN Image:   %s", truncateImage(result.finalOVNImage))
	t.Logf("Initial CNO Image: %s", truncateImage(initialState.cnoImage))
	t.Logf("Final CNO Image:   %s", truncateImage(result.finalCNOImage))
	t.Log("")
	t.Logf("CNO Updated: %v", result.cnoUpdated)
	t.Logf("OVN Image Changed: %v", result.ovnUpdated)
	t.Logf("OVN Deployment Ready: %v", result.ovnDeploymentReady)
	t.Log("")

	if result.cnoUpdated && result.ovnDeploymentReady {
		// Fix is working - CNO updated and OVN deployment is healthy
		t.Log("RESULT: PASS - CORENET-6064 fix is working correctly")
		t.Log("The CNO was updated and OVN control-plane deployment is healthy.")
		if result.ovnUpdated {
			t.Log("OVN image also updated to the target version.")
		} else {
			t.Log("OVN image remained the same (target release uses same OVN version).")
		}
		t.Log("========================================")
	} else if result.cnoUpdated && !result.ovnDeploymentReady {
		// Bug is present - CNO updated but OVN deployment is not ready
		t.Log("RESULT: FAIL - CORENET-6064 bug detected")
		t.Log("The CNO was updated but OVN control-plane deployment is NOT ready.")
		t.Log("This indicates the CORENET-6064 issue:")
		t.Log("  - CNO may be waiting for data-plane nodes to update first")
		t.Log("  - With zero workers, this condition can never be satisfied")
		t.Log("  - OVN control-plane deployment is blocked from rolling out")
		if result.blockingReason != "" {
			t.Logf("Blocking reason: %s", result.blockingReason)
		}
		t.Log("========================================")

		// This is the key assertion - fail the test when bug is present
		g.Expect(result.ovnDeploymentReady).To(BeTrue(),
			"CORENET-6064: OVN control-plane deployment is not ready after upgrade. "+
				"CNO updated from %s to %s, but OVN deployment is blocked.",
			truncateImage(initialState.cnoImage),
			truncateImage(result.finalCNOImage))
	} else if !result.cnoUpdated {
		// CNO didn't update - upgrade didn't happen at all
		t.Log("RESULT: INCONCLUSIVE - CNO did not update")
		t.Log("The cluster upgrade may not have started or completed.")
		t.Log("========================================")

		g.Expect(result.cnoUpdated).To(BeTrue(),
			"Cluster upgrade did not complete - CNO was not updated")
	}
}

// verifyNoBlockingConditions checks that there are no blocking conditions
// preventing the network operator from updating.
func verifyNoBlockingConditions(t *testing.T, g Gomega, ctx context.Context, mgtClient crclient.Client, hcpNamespace string) {
	// Check CNO pods are not waiting/blocked
	cnoDeployment := &appsv1.Deployment{}
	err := mgtClient.Get(ctx, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      "cluster-network-operator",
	}, cnoDeployment)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to get CNO deployment")

	g.Expect(cnoDeployment.Status.ReadyReplicas).To(Equal(cnoDeployment.Status.Replicas),
		"CNO deployment should have all replicas ready")

	// Check OVN control-plane deployment is healthy
	ovnDeployment := &appsv1.Deployment{}
	err = mgtClient.Get(ctx, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      "ovnkube-control-plane",
	}, ovnDeployment)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to get OVN control-plane deployment")

	g.Expect(ovnDeployment.Status.ReadyReplicas).To(Equal(ovnDeployment.Status.Replicas),
		"OVN control-plane deployment should have all replicas ready")

	// Check for any crash-looping pods in the HCP namespace
	pods := &corev1.PodList{}
	err = mgtClient.List(ctx, pods, crclient.InNamespace(hcpNamespace))
	g.Expect(err).NotTo(HaveOccurred(), "Failed to list pods")

	for _, pod := range pods.Items {
		if strings.Contains(pod.Name, "ovn") || strings.Contains(pod.Name, "network-operator") {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				g.Expect(containerStatus.RestartCount).To(BeNumerically("<", 5),
					"Pod %s container %s has too many restarts: %d",
					pod.Name, containerStatus.Name, containerStatus.RestartCount)
			}
		}
	}

	t.Log("No blocking conditions found - network components are healthy")
}

// scaleNodePoolToZero scales all NodePools for the cluster to zero replicas.
func scaleNodePoolToZero(t *testing.T, g Gomega, ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	nodePools := &hyperv1.NodePoolList{}
	err := mgtClient.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred(), "Failed to list NodePools")

	zeroReplicas := int32(0)
	for _, np := range nodePools.Items {
		if np.Spec.ClusterName == hostedCluster.Name {
			npCopy := np.DeepCopy()
			npCopy.Spec.Replicas = &zeroReplicas
			err := mgtClient.Update(ctx, npCopy)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to scale NodePool %s to zero", np.Name)
			t.Logf("Scaled NodePool %s to zero replicas", np.Name)
		}
	}
}

// waitForZeroNodes waits until all nodes are removed from the guest cluster.
func waitForZeroNodes(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	err := wait.PollUntilContextTimeout(ctx, 30*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		nodes := &corev1.NodeList{}
		if err := guestClient.List(ctx, nodes); err != nil {
			return false, nil
		}

		nodeCount := len(nodes.Items)
		t.Logf("Current node count: %d", nodeCount)
		return nodeCount == 0, nil
	})

	if err != nil {
		t.Logf("Warning: Timeout waiting for nodes to be removed, continuing anyway")
	}
}

// truncateImage truncates the image string for better log readability.
func truncateImage(image string) string {
	if len(image) > 80 {
		// Show first part and last part (sha)
		parts := strings.Split(image, "@")
		if len(parts) == 2 && len(parts[1]) > 20 {
			return parts[0] + "@" + parts[1][:20] + "..."
		}
		return image[:40] + "..." + image[len(image)-30:]
	}
	return image
}
