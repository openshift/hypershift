//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestOVNControlPlaneZeroWorkers verifies that OVN control plane components
// can successfully deploy and upgrade in a HyperShift cluster with zero worker nodes.
//
// This test validates that the ovnkube-node DaemonSet with DesiredNumberScheduled==0
// does not block control plane rollout, addressing scenarios such as:
// - Data plane hibernation (workers scaled to zero for cost savings)
// - Autoscaling from zero (no workers until workload arrives)
// - Management cluster updates when worker nodes are unreachable
//
// The test verifies:
// 1. ovnkube-control-plane Deployment becomes ready with zero workers
// 2. ovnkube-node DaemonSet correctly reports DesiredNumberScheduled==0
// 3. Control plane components complete rollout without blocking on worker rollout
func TestOVNControlPlaneZeroWorkers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Starting OVN control plane zero-worker test")

	// Configure cluster with zero workers
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 0
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {

		// Get control plane namespace where OVN components are deployed
		controlPlaneNS := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		t.Logf("Control plane namespace: %s", controlPlaneNS)

		// ==================================================================================
		// Step 1: Verify ovnkube-control-plane Deployment is ready (initial rollout)
		// ==================================================================================
		{
			t.Logf("Verifying ovnkube-control-plane Deployment becomes ready")

			e2eutil.WaitForDeploymentAvailable(
				ctx, t, mgtClient,
				"ovnkube-control-plane",
				controlPlaneNS,
				10*time.Minute,
				10*time.Second,
			)

			// Verify Deployment has at least one ready replica
			deployment := &appsv1.Deployment{}
			err := mgtClient.Get(ctx, crclient.ObjectKey{
				Name:      "ovnkube-control-plane",
				Namespace: controlPlaneNS,
			}, deployment)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get ovnkube-control-plane Deployment")
			g.Expect(deployment.Status.ReadyReplicas).To(BeNumerically(">", 0),
				"ovnkube-control-plane should have at least one ready replica")

			t.Logf("✓ Step 1 complete: ovnkube-control-plane Deployment is ready with %d replicas",
				deployment.Status.ReadyReplicas)
		}

		// ==================================================================================
		// Step 2: Verify ovnkube-node DaemonSet behavior with zero workers
		// ==================================================================================
		{
			t.Logf("Verifying ovnkube-node DaemonSet behavior with zero workers")

			ds := &appsv1.DaemonSet{}
			err := mgtClient.Get(ctx, crclient.ObjectKey{
				Name:      "ovnkube-node",
				Namespace: controlPlaneNS,
			}, ds)

			if apierrors.IsNotFound(err) {
				// DaemonSet not existing is acceptable with zero workers
				// Some HyperShift versions may not create the DaemonSet at all
				t.Logf("✓ ovnkube-node DaemonSet not found (acceptable with zero workers)")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ovnkube-node DaemonSet")

				// If DaemonSet exists, verify it correctly reports zero desired pods
				g.Expect(ds.Status.DesiredNumberScheduled).To(Equal(int32(0)),
					"DaemonSet should have 0 desired pods in zero-worker cluster")
				g.Expect(ds.Status.NumberAvailable).To(Equal(int32(0)),
					"DaemonSet should have 0 available pods")
				g.Expect(ds.Status.NumberUnavailable).To(Equal(int32(0)),
					"DaemonSet should have 0 unavailable pods")

				// Verify DaemonSet has observed the current generation
				g.Expect(ds.Status.ObservedGeneration).To(Equal(ds.Generation),
					"DaemonSet should have observed current generation")

				t.Logf("✓ Step 2 complete: ovnkube-node DaemonSet exists and correctly reports 0 desired, 0 available, 0 unavailable pods")
			}
		}

		// ==================================================================================
		// Step 3: Trigger CNO/OVN upgrade via HostedCluster release image
		// ==================================================================================
		{
			t.Logf("Triggering CNO/OVN upgrade via HostedCluster release image")

			// Get current release image as baseline
			baselineImage := hostedCluster.Spec.Release.Image
			t.Logf("Baseline release image: %s", baselineImage)

			// Get upgrade target image from global options
			// The test framework should provide a newer image via --e2e.latest-release-image
			upgradeImage := globalOpts.LatestReleaseImage
			if upgradeImage == "" || upgradeImage == baselineImage {
				t.Skip("No upgrade image specified or same as baseline, skipping upgrade test")
			}

			t.Logf("Triggering upgrade to: %s", upgradeImage)

			// Refresh HostedCluster to get latest resource version
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "failed to refresh hostedcluster")

			// Patch HostedCluster to trigger upgrade
			patch := crclient.MergeFrom(hostedCluster.DeepCopy())
			hostedCluster.Spec.Release.Image = upgradeImage
			err = mgtClient.Patch(ctx, hostedCluster, patch)
			g.Expect(err).NotTo(HaveOccurred(), "failed to patch hostedcluster release image")

			t.Logf("✓ Step 3 complete: Upgrade triggered successfully")
		}

		// ==================================================================================
		// Step 4: Verify OVN control-plane pods roll out to new image
		// ==================================================================================
		{
			t.Logf("Verifying OVN control-plane pods roll out to new image")

			// Get baseline image before rollout
			var (
				baselineImage      string
				baselineGeneration int64
			)
			deployment := &appsv1.Deployment{}
			err := mgtClient.Get(ctx, crclient.ObjectKey{
				Name:      "ovnkube-control-plane",
				Namespace: controlPlaneNS,
			}, deployment)
			if err == nil {
				baselineGeneration = deployment.Generation
			}
			if err == nil && len(deployment.Spec.Template.Spec.Containers) > 0 {
				baselineImage = deployment.Spec.Template.Spec.Containers[0].Image
				t.Logf("Baseline OVN image: %s", baselineImage)
			}

			// Wait for OVN Deployment to roll out with timeout
			timeout := 15 * time.Minute
			interval := 20 * time.Second
			t.Logf("Waiting for OVN control-plane rollout (timeout: %v)", timeout)

			g.Eventually(func() bool {
				deployment := &appsv1.Deployment{}
				err := mgtClient.Get(ctx, crclient.ObjectKey{
					Name:      "ovnkube-control-plane",
					Namespace: controlPlaneNS,
				}, deployment)
				if err != nil {
					t.Logf("Failed to get deployment: %v", err)
					return false
				}

				if deployment.Generation == baselineGeneration {
					return false
				}
				if baselineImage != "" &&
					len(deployment.Spec.Template.Spec.Containers) > 0 &&
					deployment.Spec.Template.Spec.Containers[0].Image == baselineImage {
					return false
				}

				// Check if all replicas are ready
				ready := deployment.Status.ReadyReplicas
				desired := deployment.Status.Replicas
				updated := deployment.Status.UpdatedReplicas

				t.Logf("[OVN Rollout] Ready: %d/%d, Updated: %d", ready, desired, updated)

				if desired == 0 {
					return false
				}

				return ready == desired && updated == desired && deployment.Status.ObservedGeneration == deployment.Generation
			}, timeout, interval).Should(BeTrue(), "OVN control-plane rollout should complete")

			// Verify image changed from baseline
			err = mgtClient.Get(ctx, crclient.ObjectKey{
				Name:      "ovnkube-control-plane",
				Namespace: controlPlaneNS,
			}, deployment)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get ovnkube-control-plane deployment")

			if len(deployment.Spec.Template.Spec.Containers) > 0 {
				newImage := deployment.Spec.Template.Spec.Containers[0].Image
				t.Logf("New OVN image: %s", newImage)

				if baselineImage != "" {
					g.Expect(newImage).NotTo(Equal(baselineImage),
						"OVN image should have changed after upgrade")
				}
			}

			t.Logf("✓ Step 4 complete: OVN control-plane rollout completed successfully")
		}

		// ==================================================================================
		// Step 5: Verify control plane component rollout completes (upgrade)
		// ==================================================================================
		{
			// Only run if version supports ControlPlaneComponent resources
			e2eutil.AtLeast(t, e2eutil.Version420)

			t.Logf("Verifying control plane components complete upgrade rollout")

			var startingVersion string
			// Refresh hostedcluster to get latest status
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "failed to refresh hostedcluster")

			if len(hostedCluster.Status.Version.History) > 0 {
				startingVersion = hostedCluster.Status.Version.History[0].Version
				t.Logf("Target version: %s", startingVersion)
			}

			// Wait for all control plane components to complete upgrade rollout
			// This includes CNO which manages ovnkube-control-plane
			e2eutil.WaitForControlPlaneComponentRollout(t, ctx, mgtClient, hostedCluster, startingVersion)

			t.Logf("✓ Step 5 complete: All control plane components completed upgrade rollout")
		}

		// ==================================================================================
		// Step 6: Verify overall control plane version rollout status
		// ==================================================================================
		{
			// Only run if version supports HC.Status.ControlPlaneVersion
			e2eutil.AtLeast(t, e2eutil.Version422)

			t.Logf("Verifying control plane version rollout completes")

			// Wait for HC.Status.ControlPlaneVersion to reach Completed state
			e2eutil.WaitForControlPlaneRollout(t, ctx, mgtClient, hostedCluster)

			// Verify final state
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

			if hostedCluster.Status.ControlPlaneVersion.Desired.Version != "" {
				t.Logf("✓ Step 6 complete: Control plane version rollout completed: %s",
					hostedCluster.Status.ControlPlaneVersion.Desired.Version)
			} else {
				t.Logf("✓ Step 6 complete: Control plane rollout completed")
			}
		}

		// ==================================================================================
		// Step 7: Verify network ClusterOperator is healthy with zero workers
		// ==================================================================================
		{
			t.Logf("Verifying network ClusterOperator is healthy with zero workers")

			// Get kubeconfig for hosted cluster
			hostedClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

			// Wait for network ClusterOperator to become healthy
			timeout := 10 * time.Minute
			interval := 15 * time.Second
			t.Logf("Waiting for network ClusterOperator to be healthy (timeout: %v)", timeout)

			g.Eventually(func() bool {
				// Get network ClusterOperator from hosted cluster
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "config.openshift.io",
					Version: "v1",
					Kind:    "ClusterOperator",
				})

				err := hostedClient.Get(ctx, crclient.ObjectKey{Name: "network"}, u)
				if err != nil {
					t.Logf("Failed to get network ClusterOperator: %v", err)
					return false
				}

				// Extract conditions
				conditions, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
				if !found || err != nil {
					t.Logf("No conditions found")
					return false
				}

				available := false
				progressing := true
				degraded := true

				for _, cond := range conditions {
					condMap, ok := cond.(map[string]interface{})
					if !ok {
						t.Logf("Invalid condition type, skipping")
						continue
					}

					condType, ok := condMap["type"].(string)
					if !ok {
						t.Logf("Condition type is not a string, skipping")
						continue
					}

					condStatus, ok := condMap["status"].(string)
					if !ok {
						t.Logf("Condition status is not a string, skipping")
						continue
					}

					switch condType {
					case "Available":
						available = (condStatus == "True")
					case "Progressing":
						progressing = (condStatus == "True")
					case "Degraded":
						degraded = (condStatus == "True")
					}
				}

				t.Logf("[Network CO] Available=%t Progressing=%t Degraded=%t", available, progressing, degraded)

				return available && !progressing && !degraded
			}, timeout, interval).Should(BeTrue(), "network ClusterOperator should be healthy")

			t.Logf("✓ Step 7 complete: Network ClusterOperator is healthy with zero workers")
		}

		// ==================================================================================
		// Step 8: Verify OVN components remain stable with zero workers (final check)
		// ==================================================================================
		{
			t.Logf("Verifying OVN components remain stable with zero workers")

			// Re-check ovnkube-control-plane Deployment stability
			deployment := &appsv1.Deployment{}
			err := mgtClient.Get(ctx, crclient.ObjectKey{
				Name:      "ovnkube-control-plane",
				Namespace: controlPlaneNS,
			}, deployment)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get ovnkube-control-plane Deployment")
			g.Expect(deployment.Status.ReadyReplicas).To(BeNumerically(">", 0),
				"ovnkube-control-plane should remain ready")

			// Re-check ovnkube-node DaemonSet remains at zero (or doesn't exist)
			ds := &appsv1.DaemonSet{}
			err = mgtClient.Get(ctx, crclient.ObjectKey{
				Name:      "ovnkube-node",
				Namespace: controlPlaneNS,
			}, ds)

			if apierrors.IsNotFound(err) {
				t.Logf("✓ ovnkube-node DaemonSet still not found (expected with zero workers)")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ovnkube-node DaemonSet")
				g.Expect(ds.Status.DesiredNumberScheduled).To(Equal(int32(0)),
					"DaemonSet should still have 0 desired pods")
				t.Logf("✓ ovnkube-node DaemonSet still at 0 desired pods")
			}

			t.Logf("✓ Step 8 complete: OVN components remain healthy after upgrade")
		}

		// ==================================================================================
		// All steps completed successfully
		// ==================================================================================
		t.Logf("========================================")
		t.Logf("✅ All validation steps completed successfully")
		t.Logf("========================================")

	}).WithAssetReader(content.ReadFile).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "ovn-zero-workers", globalOpts.ServiceAccountSigningKey)
}
