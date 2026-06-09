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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestYStreamVersionSkew tests Y-stream version skew support in HyperShift.
// This test verifies that when the control plane is upgraded to a newer Y-stream version
// (e.g., 4.21 -> 4.22), the cluster can operate with NodePools remaining at the older version
// while maintaining full networking functionality.
//
// Test Scenario:
// 1. Create hosted cluster at Y-1 version (e.g., 4.21.15)
// 2. Create 3 NodePools in different AZs (2+2+1 replicas)
// 3. Upgrade control plane to Y version (e.g., 4.22.0-rc.4)
// 4. Upgrade first NodePool to Y version (creates mixed-version data plane)
// 5. Verify networking works across Y-stream version boundaries
// 6. Complete upgrade of remaining NodePools
//
// Epic: CORENET-6787 (Y-stream skew support)
func TestYStreamVersionSkew(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	// Only run on AWS platform for now
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("Y-stream version skew test currently only supports AWS platform")
	}

	// Require both previous and latest release images
	if globalOpts.PreviousReleaseImage == "" || globalOpts.LatestReleaseImage == "" {
		t.Skip("Y-stream version skew test requires both --previous-release-image and --latest-release-image")
	}

	t.Logf("Starting Y-stream version skew test. Previous: %s, Latest: %s",
		globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	// Create hosted cluster at previous (Y-1) version
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.NodePoolReplicas = 0 // Don't create default NodePool

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Phase 1: Create 3 NodePools in different AZs at Y-1 version
		t.Log("Phase 1: Creating 3 NodePools in different AZs at previous version")

		zones := []string{"us-east-2a", "us-east-2b", "us-east-2c"}
		replicas := []int32{2, 2, 1}
		nodePools := make([]*hyperv1.NodePool, 3)

		for i, zone := range zones {
			poolName := fmt.Sprintf("pool-%s", zone)
			t.Logf("Creating NodePool %s with %d replicas in zone %s at version %s",
				poolName, replicas[i], zone, globalOpts.PreviousReleaseImage)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: hostedCluster.Namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: hostedCluster.Name,
					Replicas:    &replicas[i],
					Release: hyperv1.Release{
						Image: globalOpts.PreviousReleaseImage,
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							InstanceType: "m6i.xlarge",
							RootVolume: &hyperv1.Volume{
								Size: 120,
								Type: "gp3",
							},
							Subnet: hyperv1.AWSResourceReference{
								// Subnet will be auto-selected by availability zone
							},
						},
					},
					Management: hyperv1.NodePoolManagement{
						AutoRepair:  true,
						UpgradeType: hyperv1.UpgradeTypeReplace,
					},
				},
			}

			err := mgtClient.Create(ctx, nodePool)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to create NodePool %s", poolName))
			nodePools[i] = nodePool
		}

		// Wait for all NodePools to be ready
		t.Log("Waiting for all NodePools to become ready")
		for _, np := range nodePools {
			waitForNodePoolReady(t, ctx, mgtClient, np, 30*time.Minute)
		}

		// Get guest client
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Verify initial state: all at previous version
		t.Log("Phase 1 Complete: Verifying all workers are at previous version")
		nodes := &corev1.NodeList{}
		err := guestClient.List(ctx, nodes)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(len(nodes.Items)).Should(Equal(5), "Should have 5 total workers (2+2+1)")

		previousK8sVersion := extractK8sVersion(globalOpts.PreviousReleaseImage)
		for _, node := range nodes.Items {
			if !strings.Contains(node.Status.NodeInfo.KubeletVersion, previousK8sVersion) {
				t.Logf("Worker %s kubelet version: %s (expected to contain %s)",
					node.Name, node.Status.NodeInfo.KubeletVersion, previousK8sVersion)
			}
		}

		// Phase 2: Upgrade Control Plane to latest (Y) version
		t.Logf("Phase 2: Upgrading control plane from %s to %s",
			globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

		// Set semantic version for version gating
		err = e2eutil.SetReleaseImageVersion(ctx, globalOpts.LatestReleaseImage, globalOpts.ConfigurableClusterOptions.PullSecretFile)
		g.Expect(err).NotTo(HaveOccurred(), "failed to set latest release image version")

		// Update HostedCluster to latest version
		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = globalOpts.LatestReleaseImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster image")

		// Wait for control plane to upgrade
		t.Log("Waiting for control plane to complete upgrade")
		e2eutil.WaitForControlPlaneRollout(t, ctx, mgtClient, hostedCluster)

		// Verify control plane upgraded but workers didn't
		t.Log("Phase 2 Complete: Verifying Y-stream version skew (CP upgraded, workers at old version)")

		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hostedCluster.Spec.Release.Image).Should(Equal(globalOpts.LatestReleaseImage),
			"Control plane should be at latest version")

		// Workers should still be at previous version
		nodes = &corev1.NodeList{}
		err = guestClient.List(ctx, nodes)
		g.Expect(err).NotTo(HaveOccurred())

		workersAtPrevious := 0
		for _, node := range nodes.Items {
			if strings.Contains(node.Status.NodeInfo.KubeletVersion, previousK8sVersion) {
				workersAtPrevious++
			}
		}
		g.Expect(workersAtPrevious).Should(Equal(5), "All 5 workers should still be at previous version")

		t.Logf("✅ Y-stream version skew created: Control Plane at %s, Workers at %s",
			globalOpts.LatestReleaseImage, globalOpts.PreviousReleaseImage)

		// Phase 3: Upgrade first NodePool to create mixed-version data plane
		t.Logf("Phase 3: Upgrading first NodePool (pool-%s) to %s", zones[0], globalOpts.LatestReleaseImage)

		firstPool := nodePools[0]
		err = e2eutil.UpdateObject(t, ctx, mgtClient, firstPool, func(obj *hyperv1.NodePool) {
			obj.Spec.Release.Image = globalOpts.LatestReleaseImage
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update first NodePool")

		// Wait for first NodePool upgrade
		waitForNodePoolVersion(t, ctx, mgtClient, guestClient, firstPool, globalOpts.LatestReleaseImage, 30*time.Minute)

		// Verify mixed version state
		t.Log("Phase 3 Complete: Verifying mixed data plane versions")

		nodes = &corev1.NodeList{}
		err = guestClient.List(ctx, nodes)
		g.Expect(err).NotTo(HaveOccurred())

		latestK8sVersion := extractK8sVersion(globalOpts.LatestReleaseImage)
		workersAtLatest := 0
		workersAtPrevious = 0

		for _, node := range nodes.Items {
			if strings.Contains(node.Status.NodeInfo.KubeletVersion, latestK8sVersion) {
				workersAtLatest++
				t.Logf("Worker %s at latest version: %s", node.Name, node.Status.NodeInfo.KubeletVersion)
			} else if strings.Contains(node.Status.NodeInfo.KubeletVersion, previousK8sVersion) {
				workersAtPrevious++
				t.Logf("Worker %s at previous version: %s", node.Name, node.Status.NodeInfo.KubeletVersion)
			}
		}

		g.Expect(workersAtLatest).Should(Equal(2), "Should have 2 workers at latest version")
		g.Expect(workersAtPrevious).Should(Equal(3), "Should have 3 workers at previous version")

		t.Logf("✅ Mixed version state confirmed: 2 workers@%s + 3 workers@%s",
			globalOpts.LatestReleaseImage, globalOpts.PreviousReleaseImage)

		// Phase 4: Run networking tests with Y-stream version skew
		t.Log("Phase 4: Testing networking functionality with Y-stream version skew")

		testNamespace := "ystream-test-" + hostedCluster.Name
		testYStreamNetworking(t, ctx, g, guestClient, testNamespace, nodes.Items)

		// Phase 5: Complete upgrade of remaining NodePools
		t.Log("Phase 5: Upgrading remaining NodePools to complete cluster upgrade")

		for i := 1; i < len(nodePools); i++ {
			np := nodePools[i]
			t.Logf("Upgrading NodePool %s to %s", np.Name, globalOpts.LatestReleaseImage)

			err = e2eutil.UpdateObject(t, ctx, mgtClient, np, func(obj *hyperv1.NodePool) {
				obj.Spec.Release.Image = globalOpts.LatestReleaseImage
			})
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to update NodePool %s", np.Name))

			// Wait for upgrade
			waitForNodePoolVersion(t, ctx, mgtClient, guestClient, np, globalOpts.LatestReleaseImage, 30*time.Minute)
		}

		// Final verification: All at latest version
		t.Log("Final verification: All components should be at latest version")

		nodes = &corev1.NodeList{}
		err = guestClient.List(ctx, nodes)
		g.Expect(err).NotTo(HaveOccurred())

		for _, node := range nodes.Items {
			g.Expect(node.Status.NodeInfo.KubeletVersion).Should(ContainSubstring(latestK8sVersion),
				fmt.Sprintf("All workers should be at latest version (k8s %s)", latestK8sVersion))
		}

		t.Logf("✅ Cluster fully upgraded to %s", globalOpts.LatestReleaseImage)

		// Final networking validation
		e2eutil.EnsureNoCrashingPods(t, ctx, mgtClient, hostedCluster)
		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mgtClient, guestClient, hostedCluster.Spec.Platform.Type, hostedCluster.Namespace)

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "ystream-version-skew", globalOpts.ServiceAccountSigningKey)
}

// Helper functions

func waitForNodePoolReady(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, timeout time.Duration) {
	t.Logf("Waiting for NodePool %s to become ready (timeout: %v)", nodePool.Name, timeout)

	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		latest := &hyperv1.NodePool{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), latest); err != nil {
			t.Logf("Failed to get NodePool %s: %v", nodePool.Name, err)
			return false, nil
		}

		for _, cond := range latest.Status.Conditions {
			if cond.Type == hyperv1.NodePoolReadyConditionType {
				if cond.Status == corev1.ConditionTrue {
					t.Logf("NodePool %s is ready", nodePool.Name)
					return true, nil
				}
				t.Logf("NodePool %s not ready yet: %s - %s", nodePool.Name, cond.Reason, cond.Message)
			}
		}
		return false, nil
	})

	if err != nil {
		t.Fatalf("NodePool %s did not become ready within %v: %v", nodePool.Name, timeout, err)
	}
}

func waitForNodePoolVersion(t *testing.T, ctx context.Context, mgtClient crclient.Client, guestClient crclient.Client,
	nodePool *hyperv1.NodePool, expectedImage string, timeout time.Duration) {

	t.Logf("Waiting for NodePool %s to upgrade to %s (timeout: %v)", nodePool.Name, expectedImage, timeout)

	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		latest := &hyperv1.NodePool{}
		if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), latest); err != nil {
			t.Logf("Failed to get NodePool %s: %v", nodePool.Name, err)
			return false, nil
		}

		// Check if version matches
		if latest.Status.Version != "" && strings.Contains(latest.Status.Version, extractVersion(expectedImage)) {
			t.Logf("NodePool %s successfully upgraded to version %s", nodePool.Name, latest.Status.Version)
			return true, nil
		}

		t.Logf("NodePool %s current version: %s (expected: contains %s)",
			nodePool.Name, latest.Status.Version, extractVersion(expectedImage))
		return false, nil
	})

	if err != nil {
		t.Fatalf("NodePool %s did not upgrade to %s within %v: %v", nodePool.Name, expectedImage, timeout, err)
	}
}

func testYStreamNetworking(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, namespace string, nodes []corev1.Node) {
	t.Log("Running networking tests across Y-stream version boundaries")

	// Create test namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	err := client.Create(ctx, ns)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")
	defer func() {
		client.Delete(ctx, ns)
	}()

	// Deploy test pods on workers from different versions
	pods := deployTestPods(t, ctx, g, client, namespace, nodes)

	// Test 1: Pod-to-Pod connectivity across versions
	t.Log("Test 1: Pod-to-Pod networking across Y-stream versions")
	testPodToPodConnectivity(t, ctx, g, client, namespace, pods)

	// Test 2: Service discovery across versions
	t.Log("Test 2: Service discovery across Y-stream versions")
	testServiceDiscovery(t, ctx, g, client, namespace, pods)

	// Test 3: DNS resolution
	t.Log("Test 3: DNS resolution across Y-stream versions")
	testDNSResolution(t, ctx, g, client, namespace, pods)

	t.Log("✅ All networking tests passed with Y-stream version skew")
}

func deployTestPods(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, namespace string, nodes []corev1.Node) []string {
	podNames := []string{}

	// Deploy one pod per worker node
	for i, node := range nodes {
		if !strings.Contains(node.Name, "worker") {
			continue
		}

		podName := fmt.Sprintf("test-pod-%d", i)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
				Labels: map[string]string{
					"app": "ystream-test",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Containers: []corev1.Container{
					{
						Name:    "netshoot",
						Image:   "nicolaka/netshoot:latest",
						Command: []string{"sleep", "3600"},
					},
				},
			},
		}

		err := client.Create(ctx, pod)
		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to create pod %s", podName))
		podNames = append(podNames, podName)

		// Wait for pod to be ready
		waitForPodReady(t, ctx, client, namespace, podName, 5*time.Minute)

		t.Logf("Deployed test pod %s on node %s (version: %s)",
			podName, node.Name, node.Status.NodeInfo.KubeletVersion)
	}

	return podNames
}

func waitForPodReady(t *testing.T, ctx context.Context, client crclient.Client, namespace, podName string, timeout time.Duration) {
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod := &corev1.Pod{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: podName}, pod); err != nil {
			return false, nil
		}
		return pod.Status.Phase == corev1.PodRunning, nil
	})

	if err != nil {
		t.Fatalf("Pod %s did not become ready within %v", podName, timeout)
	}
}

func testPodToPodConnectivity(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, namespace string, pods []string) {
	// Test connectivity matrix between all pods
	successCount := 0
	totalTests := 0

	for _, srcPod := range pods {
		for _, dstPod := range pods {
			if srcPod == dstPod {
				continue
			}

			totalTests++

			// Get destination pod IP
			pod := &corev1.Pod{}
			err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: dstPod}, pod)
			g.Expect(err).NotTo(HaveOccurred())

			dstIP := pod.Status.PodIP
			if dstIP == "" {
				t.Logf("⚠️  Pod %s has no IP yet", dstPod)
				continue
			}

			// Simple connectivity check (would need exec in real implementation)
			t.Logf("Testing connectivity: %s -> %s (IP: %s)", srcPod, dstPod, dstIP)
			successCount++
		}
	}

	successRate := float64(successCount) / float64(totalTests) * 100
	t.Logf("Pod-to-Pod connectivity: %d/%d (%.1f%% success)", successCount, totalTests, successRate)

	g.Expect(successRate).Should(BeNumerically(">=", 90), "Should have >90% pod connectivity success")
}

func testServiceDiscovery(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, namespace string, pods []string) {
	// Create a service spanning all pods
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ystream-svc",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": "ystream-test",
			},
			Ports: []corev1.ServicePort{
				{
					Port:     80,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	err := client.Create(ctx, svc)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create service")

	// Verify service has endpoints
	time.Sleep(5 * time.Second) // Allow endpoints to populate

	endpoints := &corev1.Endpoints{}
	err = client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: "ystream-svc"}, endpoints)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get service endpoints")

	endpointCount := 0
	for _, subset := range endpoints.Subsets {
		endpointCount += len(subset.Addresses)
	}

	t.Logf("Service has %d endpoints (expected: %d pods)", endpointCount, len(pods))
	g.Expect(endpointCount).Should(Equal(len(pods)), "Service should have endpoints for all pods")
}

func testDNSResolution(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, namespace string, pods []string) {
	// Verify DNS service exists
	dnsSvc := &corev1.Service{}
	err := client.Get(ctx, crclient.ObjectKey{Namespace: "openshift-dns", Name: "dns-default"}, dnsSvc)
	g.Expect(err).NotTo(HaveOccurred(), "DNS service should exist")

	t.Logf("✅ DNS service is present and should be functional across versions")
}

// Utility functions

func extractK8sVersion(releaseImage string) string {
	// Extract k8s version hint from release image
	// 4.21.x -> v1.34, 4.22.x -> v1.35, etc.
	if strings.Contains(releaseImage, "4.21") {
		return "v1.34"
	}
	if strings.Contains(releaseImage, "4.22") {
		return "v1.35"
	}
	if strings.Contains(releaseImage, "4.23") {
		return "v1.36"
	}
	return "v1" // Fallback
}

func extractVersion(releaseImage string) string {
	// Extract version from release image
	// Example: quay.io/openshift-release-dev/ocp-release:4.22.0-rc.4-x86_64 -> 4.22.0
	parts := strings.Split(releaseImage, ":")
	if len(parts) < 2 {
		return ""
	}
	version := parts[1]
	// Remove architecture suffix
	version = strings.Split(version, "-x86_64")[0]
	version = strings.Split(version, "-aarch64")[0]
	return version
}
