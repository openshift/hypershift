//go:build e2e
// +build e2e

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
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestAzureNetworkPolicy validates that NetworkPolicies work correctly on ARO HCP (Azure) clusters.
// This test ensures that network isolation and allowed traffic paths function as expected when
// NetworkPolicies are applied to user workloads in the guest cluster.
//
// Test flow:
// 1. Create two namespaces in the guest cluster with unique names
// 2. Deploy a server pod and service in the isolated namespace
// 3. Deploy client pods in both namespaces
// 4. Verify baseline connectivity (no NetworkPolicy applied)
// 5. Apply deny-all NetworkPolicy and verify all traffic is blocked
// 6. Apply selective allow NetworkPolicy and verify only allowed traffic succeeds
func TestAzureNetworkPolicy(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("Skipping test because it requires Azure platform")
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Get guest cluster client - this is where we'll test NetworkPolicies
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Wait for nodes to be ready
		numNodes := clusterOpts.NodePoolReplicas
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Run NetworkPolicy validation tests
		testNetworkPolicyDenyThenAllow(t, ctx, g, guestClient)

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "azure-networkpolicy", globalOpts.ServiceAccountSigningKey)
}

// testNetworkPolicyDenyThenAllow tests NetworkPolicy deny-then-allow scenarios.
// This function creates namespaces, deploys workloads, and validates NetworkPolicy enforcement.
func testNetworkPolicyDenyThenAllow(t *testing.T, ctx context.Context, g Gomega, guestClient crclient.Client) {
	// Generate unique namespace names to avoid conflicts with concurrent tests
	isolatedNs := e2eutil.SimpleNameGenerator.GenerateName("netpol-isolated-")
	allowedNs := e2eutil.SimpleNameGenerator.GenerateName("netpol-allowed-")

	t.Logf("Creating test namespaces: %s (isolated) and %s (allowed)", isolatedNs, allowedNs)

	// Create namespaces with explicit labels for NetworkPolicy selectors
	// Set up defer cleanup immediately to ensure cleanup even if test fails
	createNamespaceWithLabels(ctx, t, g, guestClient, isolatedNs, map[string]string{
		"netpol-test": "isolated",
	})
	defer func() {
		t.Logf("Cleaning up namespace: %s", isolatedNs)
		_ = guestClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: isolatedNs}})
	}()

	createNamespaceWithLabels(ctx, t, g, guestClient, allowedNs, map[string]string{
		"netpol-test": "allowed",
	})
	defer func() {
		t.Logf("Cleaning up namespace: %s", allowedNs)
		_ = guestClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: allowedNs}})
	}()

	// Deploy server pod and service in isolated namespace
	t.Logf("Deploying server pod and service in %s", isolatedNs)
	deployTestServer(ctx, t, g, guestClient, isolatedNs)
	waitForPodReady(ctx, t, g, guestClient, isolatedNs, "netpol-server")

	// Deploy client pods in both namespaces
	t.Logf("Deploying client pods in both namespaces")
	deployTestClient(ctx, t, g, guestClient, isolatedNs, "netpol-client-isolated")
	deployTestClient(ctx, t, g, guestClient, allowedNs, "netpol-client-allowed")
	waitForPodReady(ctx, t, g, guestClient, isolatedNs, "netpol-client-isolated")
	waitForPodReady(ctx, t, g, guestClient, allowedNs, "netpol-client-allowed")

	// Test Scenario 1: Baseline - verify connectivity works without NetworkPolicy
	t.Logf("=== Test Scenario 1: Baseline (No NetworkPolicy) ===")
	testConnectivityWithRetry(ctx, t, g, guestClient, allowedNs, "netpol-server", isolatedNs, true)
	testConnectivityWithRetry(ctx, t, g, guestClient, isolatedNs, "netpol-server", isolatedNs, true)

	// Test Scenario 2: Default Deny - apply deny-all NetworkPolicy and verify traffic is blocked
	t.Logf("=== Test Scenario 2: Default Deny NetworkPolicy ===")
	createDenyAllNetworkPolicy(ctx, t, g, guestClient, isolatedNs)
	testConnectivityWithRetry(ctx, t, g, guestClient, allowedNs, "netpol-server", isolatedNs, false)
	testConnectivityWithRetry(ctx, t, g, guestClient, isolatedNs, "netpol-server", isolatedNs, false)

	// Delete deny-all policy before creating allow policy
	deleteDenyAllNetworkPolicy(ctx, t, g, guestClient, isolatedNs)

	// Test Scenario 3: Selective Allow - apply allow policy and verify only allowed traffic succeeds
	t.Logf("=== Test Scenario 3: Selective Allow NetworkPolicy ===")
	createAllowFromNamespaceNetworkPolicy(ctx, t, g, guestClient, isolatedNs, allowedNs)
	testConnectivityWithRetry(ctx, t, g, guestClient, allowedNs, "netpol-server", isolatedNs, true)
	testConnectivityWithRetry(ctx, t, g, guestClient, isolatedNs, "netpol-server", isolatedNs, false)

	t.Logf("✓ All NetworkPolicy tests passed successfully")
}

// createNamespaceWithLabels creates a namespace with explicit labels for NetworkPolicy selectors.
func createNamespaceWithLabels(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, name string, labels map[string]string) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
	err := client.Create(ctx, ns)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create namespace %s", name)
	t.Logf("Created namespace %s with labels %v", name, labels)
}

// deployTestServer deploys the HTTP server pod and service in the specified namespace.
func deployTestServer(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, namespace string) {
	// Create server pod
	serverPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "netpol-server",
			Namespace: namespace,
			Labels: map[string]string{
				"app": "netpol-server",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agnhost",
					Image: "registry.k8s.io/e2e-test-images/agnhost:2.43",
					Args:  []string{"netexec", "--http-port=8080"},
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 8080,
							Name:          "http",
						},
					},
				},
			},
		},
	}
	err := client.Create(ctx, serverPod)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create server pod")
	t.Logf("Created server pod %s/%s", namespace, serverPod.Name)

	// Create service
	serverSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "netpol-server",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": "netpol-server",
			},
			Ports: []corev1.ServicePort{
				{
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	err = client.Create(ctx, serverSvc)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create server service")
	t.Logf("Created server service %s/%s", namespace, serverSvc.Name)
}

// deployTestClient deploys a client pod for connectivity testing.
func deployTestClient(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, namespace, name string) {
	clientPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "netpol-client",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "agnhost",
					Image:   "registry.k8s.io/e2e-test-images/agnhost:2.43",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
	err := client.Create(ctx, clientPod)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create client pod %s/%s", namespace, name)
	t.Logf("Created client pod %s/%s", namespace, name)
}

// waitForPodReady waits for a pod to be ready using retry logic.
func waitForPodReady(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, namespace, podName string) {
	t.Logf("Waiting for pod %s/%s to be ready", namespace, podName)
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		pod := &corev1.Pod{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: podName}, pod); err != nil {
			return false, nil // Pod not found yet, keep retrying
		}

		// Check if pod is running and ready
		if pod.Status.Phase != corev1.PodRunning {
			return false, nil
		}

		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil // Pod is ready!
			}
		}

		return false, nil // Not ready yet
	})
	g.Expect(err).ToNot(HaveOccurred(), "pod %s/%s did not become ready within timeout", namespace, podName)
	t.Logf("Pod %s/%s is ready", namespace, podName)
}

// testConnectivityWithRetry tests connectivity from a client pod to a target service with retry logic.
// For expected success: retries for up to 2 minutes to allow NetworkPolicy propagation.
// For expected failure: retries for up to 30 seconds to ensure consistent denial.
func testConnectivityWithRetry(ctx context.Context, t *testing.T, g Gomega, client crclient.Client,
	clientNs, targetSvc, targetNs string, expectSuccess bool) {

	targetURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", targetSvc, targetNs)
	expectation := "FAILURE"
	if expectSuccess {
		expectation = "SUCCESS"
	}

	t.Logf("Testing connectivity from %s/netpol-client to %s (expect: %s)", clientNs, targetURL, expectation)

	// Curl command with short timeout to fail fast
	command := []string{
		"curl",
		"--connect-timeout", "5",
		"--max-time", "10",
		"-s",
		"-o", "/dev/null",
		"-w", "%{http_code}",
		targetURL,
	}

	var timeout time.Duration
	var interval time.Duration
	if expectSuccess {
		timeout = 2 * time.Minute  // Allow time for NetworkPolicy propagation
		interval = 5 * time.Second
	} else {
		timeout = 30 * time.Second // Shorter timeout for expected failures
		interval = 3 * time.Second
	}

	var lastOutput string
	var lastErr error

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		// Use RunCommandInPod with label matching
		output, err := e2eutil.RunCommandInPod(ctx, client, "netpol-client", clientNs, command, "agnhost", 10*time.Second)
		lastOutput = output
		lastErr = err

		if expectSuccess {
			// Expecting success: look for HTTP 200
			if err == nil && strings.TrimSpace(output) == "200" {
				return true, nil // Success!
			}
			return false, nil // Keep retrying
		} else {
			// Expecting failure: connection should timeout or be refused
			if err != nil {
				// Connection failed as expected
				return true, nil
			}
			// Connection succeeded when we expected failure - keep retrying
			// (might be policy still propagating)
			return false, nil
		}
	})

	if expectSuccess {
		g.Expect(err).ToNot(HaveOccurred(), "expected connection to succeed but it failed. Last output: %s, Last error: %v", lastOutput, lastErr)
		t.Logf("✓ Connection succeeded as expected (HTTP %s)", strings.TrimSpace(lastOutput))
	} else {
		g.Expect(err).ToNot(HaveOccurred(), "expected connection to fail but it succeeded. Last output: %s", lastOutput)
		t.Logf("✓ Connection denied as expected (error: %v)", lastErr)
	}
}

// createDenyAllNetworkPolicy creates a NetworkPolicy that denies all ingress traffic.
func createDenyAllNetworkPolicy(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, namespace string) {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-deny-ingress",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{}, // Empty selector = all pods
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			// Empty Ingress rules = deny all
		},
	}
	err := client.Create(ctx, policy)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create deny-all NetworkPolicy")
	t.Logf("Created deny-all NetworkPolicy %s/%s", namespace, policy.Name)
}

// deleteDenyAllNetworkPolicy deletes the deny-all NetworkPolicy.
func deleteDenyAllNetworkPolicy(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, namespace string) {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-deny-ingress",
			Namespace: namespace,
		},
	}
	err := client.Delete(ctx, policy)
	g.Expect(err).ToNot(HaveOccurred(), "failed to delete deny-all NetworkPolicy")
	t.Logf("Deleted deny-all NetworkPolicy %s/%s", namespace, policy.Name)
}

// createAllowFromNamespaceNetworkPolicy creates a NetworkPolicy that allows ingress from a specific namespace.
func createAllowFromNamespaceNetworkPolicy(ctx context.Context, t *testing.T, g Gomega, client crclient.Client, namespace, allowedNs string) {
	tcpProtocol := corev1.ProtocolTCP

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-from-allowed-ns",
			Namespace: namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "netpol-server",
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"netpol-test": "allowed",
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 8080},
						},
					},
				},
			},
		},
	}
	err := client.Create(ctx, policy)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create allow NetworkPolicy")
	t.Logf("Created allow-from-namespace NetworkPolicy %s/%s (allows from %s)", namespace, policy.Name, allowedNs)
}
