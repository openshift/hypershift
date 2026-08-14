//go:build e2e

package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	metricsForwarderDeploymentName = "control-plane-metrics-forwarder"
	monitoringNamespace            = "openshift-monitoring"
)

// EnsureMetricsForwarderWorking enables metrics forwarding on the HostedCluster,
// waits for the management-side metrics-proxy and endpoint-resolver deployments,
// then verifies the guest cluster's data-plane Prometheus is successfully scraping
// kube-apiserver metrics through the metrics-forwarder.
//
// The data path tested is:
//
//	guest Prometheus → PodMonitor → metrics-forwarder (HAProxy TCP passthrough) → Route → metrics-proxy → kube-apiserver
func EnsureMetricsForwarderWorking(t *testing.T, ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureMetricsForwarderWorking", func(t *testing.T) {
		AtLeast(t, Version422)
		g := NewWithT(t)
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// 1. Enable metrics forwarding via spec with metricsSet=All.
		t.Log("Enabling metrics forwarding on HostedCluster with metricsSet=All")
		err := UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
			obj.Spec.Monitoring.MetricsForwarding.MetricsSet = hyperv1.MetricsSetAll
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to enable metrics forwarding on HostedCluster")

		// 2. Wait for management-side deployments.
		t.Log("Waiting for endpoint-resolver deployment")
		WaitForDeploymentAvailable(ctx, t, mgtClient, "endpoint-resolver", hcpNamespace, 5*time.Minute, 10*time.Second)

		t.Log("Waiting for metrics-proxy deployment")
		WaitForDeploymentAvailable(ctx, t, mgtClient, "metrics-proxy", hcpNamespace, 5*time.Minute, 10*time.Second)

		// 3. Get guest cluster access.
		guestRestConfig := WaitForGuestRestConfig(t, ctx, mgtClient, hostedCluster)
		guestClient := WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// 4. Wait for the metrics-forwarder deployment in the guest cluster.
		t.Log("Waiting for metrics-forwarder deployment in guest cluster")
		WaitForDeploymentAvailable(ctx, t, guestClient, metricsForwarderDeploymentName, monitoringNamespace, 5*time.Minute, 10*time.Second)

		// 5. Wait for the prometheus pod to be running in the guest cluster.
		promPodName := waitForRunningPrometheusPod(t, ctx, g, guestClient)

		// 6. Verify Prometheus is scraping kube-apiserver metrics through the forwarder.
		t.Log("Verifying guest cluster Prometheus is scraping kube-apiserver via the metrics-forwarder")
		err = waitForKASTargetUp(t, ctx, guestRestConfig, promPodName)
		g.Expect(err).NotTo(HaveOccurred(), "kube-apiserver target via metrics-forwarder should be UP in guest cluster Prometheus")

		// 7. Query Prometheus for actual kube-apiserver metrics to confirm data was scraped.
		t.Log("Querying Prometheus for kube-apiserver metrics scraped via the metrics-forwarder")
		assertMetricPresent(t, ctx, g, guestRestConfig, promPodName,
			`apiserver_request_total{job="apiserver"}`,
			"should have apiserver_request_total metrics from kube-apiserver")

		// 8. Verify the metrics-proxy deployment has --metrics-set All in its args.
		t.Log("Verifying metrics-proxy deployment has --metrics-set All")
		waitForMetricsProxyArgs(t, ctx, g, mgtClient, hcpNamespace, string(hyperv1.MetricsSetAll), 5*time.Minute)

		// 9. Verify process_resident_memory_bytes is present with metricsSet=All.
		// This metric is always emitted by kube-apiserver but is NOT in the
		// Telemetry keep list, so its presence now and absence later proves filtering.
		t.Log("Verifying process_resident_memory_bytes is present with metricsSet=All")
		assertMetricPresent(t, ctx, g, guestRestConfig, promPodName,
			`process_resident_memory_bytes{job="apiserver"}`,
			"process_resident_memory_bytes should be present with metricsSet=All")

		// 10. Switch metricsSet to Telemetry to verify filtering.
		t.Log("Switching metricsSet to Telemetry on HostedCluster")
		err = UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Monitoring.MetricsForwarding.MetricsSet = hyperv1.MetricsSetTelemetry
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update metricsSet to Telemetry on HostedCluster")

		// 11. Wait for metrics-proxy deployment to roll out with --metrics-set Telemetry.
		t.Log("Waiting for metrics-proxy deployment to roll out with --metrics-set Telemetry")
		waitForMetricsProxyArgs(t, ctx, g, mgtClient, hcpNamespace, string(hyperv1.MetricsSetTelemetry), 5*time.Minute)

		// 12. Wait for the kube-apiserver target to be UP again after the rollout.
		t.Log("Waiting for kube-apiserver target to be UP after metricsSet change")
		err = waitForKASTargetUp(t, ctx, guestRestConfig, promPodName)
		g.Expect(err).NotTo(HaveOccurred(), "kube-apiserver target should be UP after metricsSet change")

		// 13. Verify apiserver_request_total is still present (it's in the Telemetry keep list).
		t.Log("Verifying apiserver_request_total is still present with metricsSet=Telemetry")
		assertMetricPresent(t, ctx, g, guestRestConfig, promPodName,
			`apiserver_request_total{job="apiserver"}`,
			"apiserver_request_total should be present with metricsSet=Telemetry")

		// 14. Verify process_resident_memory_bytes is now absent.
		// The Telemetry keep regex for kube-apiserver is:
		//   (apiserver_storage_objects|apiserver_request_total|apiserver_current_inflight_requests)
		// process_resident_memory_bytes does not match, so it must be filtered out.
		t.Log("Verifying process_resident_memory_bytes is absent with metricsSet=Telemetry")
		assertMetricAbsent(t, ctx, g, guestRestConfig, promPodName,
			`process_resident_memory_bytes{job="apiserver"}`,
			"process_resident_memory_bytes should be filtered out with metricsSet=Telemetry")
	})
}

// waitForMetricsProxyArgs polls the metrics-proxy Deployment until its container
// has "--metrics-set <expectedSet>" in its args and the deployment is available.
func waitForMetricsProxyArgs(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, namespace, expectedSet string, timeout time.Duration) {
	t.Helper()
	g.Eventually(func(g Gomega) {
		dep := &appsv1.Deployment{}
		g.Expect(client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: "metrics-proxy"}, dep)).To(Succeed())

		var found bool
		for _, c := range dep.Spec.Template.Spec.Containers {
			if c.Name != "metrics-proxy" {
				continue
			}
			for i, arg := range c.Args {
				if arg == "--metrics-set" && i+1 < len(c.Args) {
					g.Expect(c.Args[i+1]).To(Equal(expectedSet),
						"metrics-proxy --metrics-set arg should be %s", expectedSet)
					found = true
					break
				}
			}
		}
		g.Expect(found).To(BeTrue(), "metrics-proxy container should have --metrics-set arg")

		desired := int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		g.Expect(dep.Status.ObservedGeneration).To(Equal(dep.Generation),
			"deployment controller should have processed the latest spec")
		g.Expect(dep.Status.UpdatedReplicas).To(Equal(desired),
			"all replicas should be running the updated pod template")
		g.Expect(dep.Status.ReadyReplicas).To(Equal(desired),
			"all updated replicas should be ready")
		g.Expect(dep.Status.AvailableReplicas).To(Equal(desired),
			"all updated replicas should be available")
		g.Expect(dep.Status.UnavailableReplicas).To(Equal(int32(0)),
			"no replicas should be unavailable")
	}, timeout, 10*time.Second).Should(Succeed())
}

// waitForKASTargetUp polls the Prometheus targets API until the kube-apiserver
// target via the metrics-forwarder is UP.
func waitForKASTargetUp(t *testing.T, ctx context.Context, guestRestConfig *rest.Config, promPodName string) error {
	t.Helper()
	return wait.PollUntilContextTimeout(ctx, 15*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		output, execErr := execInGuestPod(ctx, guestRestConfig, monitoringNamespace, promPodName, "prometheus",
			[]string{"curl", "-s", "http://localhost:9090/api/v1/targets"})
		if execErr != nil {
			t.Logf("failed to query Prometheus targets API (will retry): %v", execErr)
			return false, nil
		}

		var resp targetsAPIResponse
		if err := json.Unmarshal([]byte(output), &resp); err != nil {
			t.Logf("failed to parse targets response (will retry): %v", err)
			return false, nil
		}

		if resp.Status != "success" {
			return false, nil
		}

		for _, target := range resp.Data.Active {
			if !strings.Contains(target.ScrapePool, "control-plane-metrics-forwarder") {
				continue
			}
			if !strings.Contains(target.ScrapeURL, "/metrics/kube-apiserver") {
				continue
			}
			if target.Health == prometheusv1.HealthGood {
				t.Logf("kube-apiserver target via metrics-forwarder is UP: scrapePool=%s", target.ScrapePool)
				return true, nil
			}
			t.Logf("kube-apiserver target health=%s (will retry)", target.Health)
			return false, nil
		}

		t.Log("kube-apiserver target not found in Prometheus active targets (will retry)")
		return false, nil
	})
}

// assertMetricPresent queries Prometheus for the given metric and asserts it has results.
func assertMetricPresent(t *testing.T, ctx context.Context, g Gomega, guestRestConfig *rest.Config, promPodName, query, msg string) {
	t.Helper()
	var queryResp queryAPIResponse
	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		output, execErr := execInGuestPod(ctx, guestRestConfig, monitoringNamespace, promPodName, "prometheus",
			[]string{"curl", "-gs", fmt.Sprintf("http://localhost:9090/api/v1/query?query=%s", query)})
		if execErr != nil {
			t.Logf("failed to query Prometheus for %s (will retry): %v", query, execErr)
			return false, nil
		}

		if err := json.Unmarshal([]byte(output), &queryResp); err != nil {
			t.Logf("failed to parse query response (will retry): %v", err)
			return false, nil
		}

		if queryResp.Status != "success" {
			return false, nil
		}

		return len(queryResp.Data.Result) > 0, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), msg)
	t.Logf("Found %d time series for %s", len(queryResp.Data.Result), query)
}

// assertMetricAbsent queries Prometheus for the given metric and asserts it has no results.
// It retries a few times to account for stale data being flushed after a metrics-proxy rollout.
func assertMetricAbsent(t *testing.T, ctx context.Context, g Gomega, guestRestConfig *rest.Config, promPodName, query, msg string) {
	t.Helper()
	var queryResp queryAPIResponse
	err := wait.PollUntilContextTimeout(ctx, 30*time.Second, 8*time.Minute, true, func(ctx context.Context) (bool, error) {
		output, execErr := execInGuestPod(ctx, guestRestConfig, monitoringNamespace, promPodName, "prometheus",
			[]string{"curl", "-gs", fmt.Sprintf("http://localhost:9090/api/v1/query?query=%s", query)})
		if execErr != nil {
			t.Logf("failed to query Prometheus for %s (will retry): %v", query, execErr)
			return false, nil
		}

		if err := json.Unmarshal([]byte(output), &queryResp); err != nil {
			t.Logf("failed to parse query response (will retry): %v", err)
			return false, nil
		}

		if queryResp.Status != "success" {
			return false, nil
		}

		if len(queryResp.Data.Result) > 0 {
			t.Logf("metric %s still has %d results (stale data may still be present, will retry)", query, len(queryResp.Data.Result))
			return false, nil
		}

		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), msg)
	t.Logf("Confirmed metric %s is absent", query)
}

// waitForRunningPrometheusPod waits for the prometheus-k8s-0 pod (the well-known
// first replica of the CMO-managed Prometheus StatefulSet) to be running.
func waitForRunningPrometheusPod(t *testing.T, ctx context.Context, g Gomega, guestClient crclient.Client) string {
	t.Helper()

	const podName = "prometheus-k8s-0"
	t.Logf("Waiting for prometheus pod %s/%s to be running", monitoringNamespace, podName)

	g.Eventually(func() bool {
		pod := &corev1.Pod{}
		if err := guestClient.Get(ctx, crclient.ObjectKey{Namespace: monitoringNamespace, Name: podName}, pod); err != nil {
			t.Logf("failed to get pod %s: %v", podName, err)
			return false
		}
		return pod.Status.Phase == corev1.PodRunning
	}, 5*time.Minute, 10*time.Second).Should(BeTrue(), "prometheus pod %s should be running", podName)

	t.Logf("Prometheus pod %s is running", podName)
	return podName
}

// execInGuestPod executes a command in a pod on the guest cluster.
// Unlike RunCommandInPod which uses the management cluster REST config,
// this function accepts an explicit REST config for the guest cluster.
func execInGuestPod(ctx context.Context, guestConfig *rest.Config, namespace, podName, containerName string, command []string) (string, error) {
	stdOut := new(bytes.Buffer)
	stdErr := new(bytes.Buffer)
	execOpts := PodExecOptions{
		StreamOptions: StreamOptions{
			IOStreams: genericclioptions.IOStreams{
				Out:    stdOut,
				ErrOut: stdErr,
			},
		},
		Command:       command,
		Namespace:     namespace,
		PodName:       podName,
		Config:        guestConfig,
		ContainerName: containerName,
		Timeout:       30 * time.Second,
	}

	if err := execOpts.Run(ctx); err != nil {
		return "", fmt.Errorf("exec in %s/%s container %s failed: %w (stderr: %s)", namespace, podName, containerName, err, stdErr.String())
	}
	return stdOut.String(), nil
}

// targetsAPIResponse wraps the Prometheus /api/v1/targets JSON envelope
// around the upstream TargetsResult type.
type targetsAPIResponse struct {
	Status string                     `json:"status"`
	Data   prometheusv1.TargetsResult `json:"data"`
}

// queryAPIResponse wraps the Prometheus /api/v1/query JSON envelope
// around a model.Vector result.
type queryAPIResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string       `json:"resultType"`
		Result     model.Vector `json:"result"`
	} `json:"data"`
}
