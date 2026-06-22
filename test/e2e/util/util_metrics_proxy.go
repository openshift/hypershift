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

		// 1. Enable metrics forwarding by adding the annotation.
		t.Log("Enabling metrics forwarding on HostedCluster")
		err := UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.EnableMetricsForwarding] = "true"
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to patch HostedCluster with EnableMetricsForwarding annotation")

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
		// Poll the Prometheus targets API from inside the prometheus pod until
		// the kube-apiserver target (via the metrics-forwarder PodMonitor) is UP.
		t.Log("Verifying guest cluster Prometheus is scraping kube-apiserver via the metrics-forwarder")
		err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
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
				t.Logf("targets API returned status %q (will retry)", resp.Status)
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
					t.Logf("kube-apiserver target via metrics-forwarder is UP: scrapePool=%s, lastScrape=%s, scrapeURL=%s",
						target.ScrapePool, target.LastScrape, target.ScrapeURL)
					return true, nil
				}
				t.Logf("kube-apiserver target found but health=%s, lastError=%s (will retry)",
					target.Health, target.LastError)
				return false, nil
			}

			t.Log("kube-apiserver target via metrics-forwarder not found in Prometheus active targets (will retry)")
			return false, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "kube-apiserver target via metrics-forwarder should be UP in guest cluster Prometheus")

		// 7. Query Prometheus for actual kube-apiserver metrics to confirm data was scraped.
		t.Log("Querying Prometheus for kube-apiserver metrics scraped via the metrics-forwarder")
		output, err := execInGuestPod(ctx, guestRestConfig, monitoringNamespace, promPodName, "prometheus",
			[]string{"curl", "-gs", "http://localhost:9090/api/v1/query?query=apiserver_request_total{job=\"apiserver\"}"})
		g.Expect(err).NotTo(HaveOccurred(), "failed to query Prometheus for apiserver_request_total")

		var queryResp queryAPIResponse
		g.Expect(json.Unmarshal([]byte(output), &queryResp)).To(Succeed(), "failed to parse Prometheus query response")
		g.Expect(queryResp.Status).To(Equal("success"), "Prometheus query should succeed")
		g.Expect(queryResp.Data.Result).NotTo(BeEmpty(), "should have apiserver_request_total metrics from kube-apiserver")

		t.Logf("Found %d apiserver_request_total time series in guest cluster Prometheus", len(queryResp.Data.Result))
	})
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
