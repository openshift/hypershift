//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	securityv1 "github.com/openshift/api/security/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCiliumConnectivity validates Cilium connectivity using the official Cilium connectivity test suite.
// This test is specifically for ARO HCP clusters with Cilium as the network provider.
//
// The test performs the following steps:
// 1.Check cilium agent pods ready
// 2. Creates cilium-test namespace with appropriate labels
// 3. Deploys Cilium connectivity test pods
// 4. Waits for all test pods to be ready and running
// 5. Cleans up test resources.
func TestCiliumConnectivity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("Skipping test because it requires Azure platform")
	}

	if globalOpts.ExternalCNIProvider != "cilium" {
		t.Skipf("skip cilium connection test if e2e.external-cni-provider is not cilium")
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		if !azureutil.IsAroHCP() {
			t.Skip("test only supported on ARO HCP clusters")
		}
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		testNamespace := "cilium-test"

		// Cleanup function
		cleanup := func() {
			t.Log("Cleaning up Cilium connectivity test resources")

			// Delete SCC
			scc := &securityv1.SecurityContextConstraints{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cilium-test",
				},
			}
			if err := guestClient.Delete(ctx, scc); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("Warning: failed to delete SCC: %v", err)
			}

			// Delete namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}
			if err := guestClient.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("Warning: failed to delete namespace: %v", err)
			}

			t.Log("Cleanup completed")
		}
		defer cleanup()

		t.Run("CheckCiliumPodsRunning", func(t *testing.T) {
			g := NewWithT(t)

			t.Log("Check cilium pods to be running")
			g.Eventually(func() bool {
				podList := &corev1.PodList{}
				err := guestClient.List(ctx, podList, crclient.InNamespace("cilium"))
				if err != nil {
					t.Logf("Failed to list Cilium pods: %v", err)
					return false
				}
				t.Logf("Found %d Cilium pods", len(podList.Items))

				// Check all pods are Ready
				notReadyPods := []string{}
				for _, pod := range podList.Items {
					ready := false
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
							ready = true
							break
						}
					}
					if !ready {
						notReadyPods = append(notReadyPods, fmt.Sprintf("%s (phase: %s)", pod.Name, pod.Status.Phase))
					}
				}
				if len(notReadyPods) > 0 {
					t.Logf("Pods not ready: %v", notReadyPods)
					return false
				}
				return true
			}, e2eutil.CiliumPodsReadyTimeout, e2eutil.CiliumPodsReadyPollInterval).Should(BeTrue(), "all Cilium pods should be running")

			t.Log("All Cilium pods are running")
		})

		t.Run("CreateSecurityContextConstraints", func(t *testing.T) {
			g := NewWithT(t)

			t.Log("Creating SecurityContextConstraints for Cilium connectivity test")
			scc := &securityv1.SecurityContextConstraints{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cilium-test",
				},
				AllowHostPorts:           true,
				AllowHostNetwork:         true,
				AllowHostDirVolumePlugin: false,
				AllowHostIPC:             false,
				AllowHostPID:             false,
				AllowPrivilegeEscalation: ptrBool(false),
				AllowPrivilegedContainer: false,
				ReadOnlyRootFilesystem:   false,
				RequiredDropCapabilities: []corev1.Capability{},
				RunAsUser:                securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyMustRunAsRange},
				SELinuxContext:           securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyMustRunAs},
				Users:                    []string{"system:serviceaccount:cilium-test:default"},
			}

			err := guestClient.Create(ctx, scc)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				g.Expect(err).ToNot(HaveOccurred(), "failed to create SCC")
			}

			t.Log("SecurityContextConstraints created successfully")
		})

		t.Run("CreateTestNamespace", func(t *testing.T) {
			g := NewWithT(t)

			t.Log("Creating cilium-test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
					Labels: map[string]string{
						"security.openshift.io/scc.podSecurityLabelSync": "false",
						"pod-security.kubernetes.io/enforce":             "privileged",
						"pod-security.kubernetes.io/audit":               "privileged",
						"pod-security.kubernetes.io/warn":                "privileged",
					},
				},
			}

			err := guestClient.Create(ctx, ns)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				g.Expect(err).ToNot(HaveOccurred(), "failed to create namespace")
			}

			t.Log("Test namespace created successfully")
		})

		t.Run("DeployCiliumConnectivityTest", func(t *testing.T) {
			g := NewWithT(t)

			t.Logf("Deploying Cilium connectivity test from version %s", e2eutil.CiliumVersion)
			connectivityTestURL := fmt.Sprintf("https://raw.githubusercontent.com/cilium/cilium/%s/examples/kubernetes/connectivity-check/connectivity-check.yaml", e2eutil.CiliumVersion)

			err := e2eutil.ApplyYAMLFromURL(ctx, guestClient, connectivityTestURL, testNamespace)
			g.Expect(err).ToNot(HaveOccurred(), "failed to apply connectivity test manifest")

			t.Log("Connectivity test manifests applied successfully")
		})

		t.Run("WaitForConnectivityTestPodsReady", func(t *testing.T) {
			g := NewWithT(t)

			t.Log("Waiting for connectivity test pods to be ready")
			g.Eventually(func() bool {
				podList := &corev1.PodList{}
				err := guestClient.List(ctx, podList, crclient.InNamespace(testNamespace))
				if err != nil {
					t.Logf("Failed to list pods: %v", err)
					return false
				}
				if len(podList.Items) == 0 {
					t.Log("No pods found in cilium-test namespace yet")
					return false
				}

				t.Logf("Found %d pods in cilium-test namespace", len(podList.Items))

				// Check all pods are Ready
				for _, pod := range podList.Items {
					ready := false
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
							ready = true
							break
						}
					}
					if !ready {
						t.Logf("Pod %s is not ready yet, phase: %s", pod.Name, pod.Status.Phase)
						return false
					}
				}
				return true
			}, e2eutil.CiliumConnectivityTestPodsTimeout, e2eutil.CiliumConnectivityTestPodsPollInterval).Should(BeTrue(), "all connectivity test pods should be ready")

			t.Log("All connectivity test pods are ready")
		})

		t.Run("WaitForConnectivityTestCompletion", func(t *testing.T) {
			g := NewWithT(t)

			t.Logf("Waiting %v for connectivity tests to run", e2eutil.CiliumConnectivityTestDuration)
			time.Sleep(e2eutil.CiliumConnectivityTestDuration)

			t.Log("Verifying all test pods are still running")
			podList := &corev1.PodList{}
			err := guestClient.List(ctx, podList, crclient.InNamespace(testNamespace))
			g.Expect(err).ToNot(HaveOccurred(), "should be able to list test pods")

			failedPods := []string{}
			for _, pod := range podList.Items {
				if pod.Status.Phase != corev1.PodRunning {
					failedPods = append(failedPods, fmt.Sprintf("%s (phase: %s)", pod.Name, pod.Status.Phase))
				}
			}

			if len(failedPods) > 0 {
				t.Errorf("Found %d pods not in Running phase: %v", len(failedPods), failedPods)
			} else {
				t.Logf("All %d connectivity test pods are running successfully", len(podList.Items))
			}
		})

		t.Log("Cilium connectivity test completed successfully")
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "cilium-connectivity", globalOpts.ServiceAccountSigningKey)
}

func ptrBool(b bool) *bool {
	return &b
}
