//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"
	azureutil "github.com/openshift/hypershift/support/azureutil"
	supportforwarder "github.com/openshift/hypershift/support/forwarder"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func expectMetricHasLabel(g Gomega, families map[string]*dto.MetricFamily, metricName, labelName, labelValue string) {
	family, ok := families[metricName]
	g.Expect(ok).To(BeTrue(), "metric %s should exist", metricName)
	hasMatch := false
	for _, m := range family.Metric {
		for _, l := range m.GetLabel() {
			if l.GetName() == labelName && l.GetValue() == labelValue {
				hasMatch = true
			}
		}
	}
	g.Expect(hasMatch).To(BeTrue(), "metric %s should have label %s=%s", metricName, labelName, labelValue)
}

func RegisterHostedClusterMetricsTests(getTestCtx internal.TestContextGetter) {
	ValidateMetricsTest(getTestCtx)
	EnsureMetricsForwarderWorkingTest(getTestCtx)
	EnsureNodeTuningOperatorMetricsEndpointTest(getTestCtx)
	EnsureKubeSchedulerMetricsEndpointTest(getTestCtx)
}

func ValidateMetricsTest(getTestCtx internal.TestContextGetter) {
	When("HyperShift operator is running", func() {
		It("should expose expected metrics at the metrics endpoint", func() {
			tc := getTestCtx()
			tc.SkipIfPlatform(hyperv1.NonePlatform)
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			mgmtRestConfig, err := e2eutil.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "should be able to load management cluster REST config")

			clientset, err := kubernetes.NewForConfig(mgmtRestConfig)
			Expect(err).NotTo(HaveOccurred(), "should be able to create kubernetes clientset")

			hoNamespace := "hypershift"
			hcName := hostedCluster.Name

			Eventually(func(g Gomega) {
				currentPods := &corev1.PodList{}
				g.Expect(tc.MgmtClient.List(tc.Context, currentPods,
					crclient.InNamespace(hoNamespace),
					crclient.MatchingLabels{"app": "operator"},
				)).To(Succeed(), "should be able to list pods in the hypershift namespace")
				g.Expect(currentPods.Items).NotTo(BeEmpty(), "hypershift-operator pod should exist")

				var runningPodName string
				for _, p := range currentPods.Items {
					if p.Status.Phase == corev1.PodRunning {
						runningPodName = p.Name
						break
					}
				}
				g.Expect(runningPodName).NotTo(BeEmpty(), "a running hypershift-operator pod should exist")

				metrics, err := v2util.GetMetricsFromPod(tc.Context, clientset, mgmtRestConfig, hoNamespace, runningPodName, "operator", 9000)
				g.Expect(err).NotTo(HaveOccurred(), "should be able to fetch metrics from hypershift-operator pod")

				g.Expect(metrics).To(HaveKey("hypershift_operator_info"),
					"metrics should contain hypershift_operator_info")

				for _, metricName := range []string{
					hcmetrics.SilenceAlertsMetricName,
					hcmetrics.LimitedSupportEnabledMetricName,
					hcmetrics.ProxyMetricName,
				} {
					expectMetricHasLabel(g, metrics, metricName, "name", hcName)
				}

				for _, metricName := range []string{
					npmetrics.SizeMetricName,
					npmetrics.AvailableReplicasMetricName,
				} {
					expectMetricHasLabel(g, metrics, metricName, "cluster_name", hcName)
				}

				if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
					expectMetricHasLabel(g, metrics, hcmetrics.InvalidAwsCredsMetricName, "name", hcName)
				}

				if hostedCluster.Spec.Platform.Type == hyperv1.AzurePlatform && azureutil.IsAroHCP() {
					family, ok := metrics[hcmetrics.HostedClusterManagedAzureInfoMetricName]
					g.Expect(ok).To(BeTrue(), "metric %s should exist on managed Azure",
						hcmetrics.HostedClusterManagedAzureInfoMetricName)
					g.Expect(family.Metric).NotTo(BeEmpty(),
						"metric %s should have at least one time series",
						hcmetrics.HostedClusterManagedAzureInfoMetricName)
				}
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func EnsureMetricsForwarderWorkingTest(getTestCtx internal.TestContextGetter) {
	When("metrics forwarding is enabled", Label("Informing"), func() {
		It("should deploy the metrics pipeline and scrape kube-apiserver metrics end-to-end", func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(e2eutil.Version422)
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			if hostedCluster.Spec.Monitoring.MetricsForwarding.Mode != hyperv1.MetricsForwardingModeForward {
				Skip("metrics forwarding not enabled on hosted cluster; skipping verification test")
			}

			By("Waiting for management-side metrics deployments")
			Eventually(func(g Gomega) {
				for _, app := range []string{"endpoint-resolver", "metrics-proxy"} {
					podList := &corev1.PodList{}
					g.Expect(tc.MgmtClient.List(tc.Context, podList,
						crclient.InNamespace(tc.ControlPlaneNamespace),
						crclient.MatchingLabels{"app": app},
					)).To(Succeed())
					g.Expect(podList.Items).NotTo(BeEmpty(), "%s pod should exist in the control plane namespace", app)
				}
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Waiting for hosted cluster metrics-forwarder deployment")
			hcClient, err := tc.GetHostedClusterClient(hostedCluster)
			Expect(err).NotTo(HaveOccurred())
			hcRestConfig, err := tc.GetHostedClusterRESTConfig(hostedCluster)
			Expect(err).NotTo(HaveOccurred())

			hcClientset, err := kubernetes.NewForConfig(hcRestConfig)
			Expect(err).NotTo(HaveOccurred(), "should be able to create hosted cluster kubernetes clientset")

			const monitoringNamespace = "openshift-monitoring"
			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(hcClient.List(tc.Context, podList,
					crclient.InNamespace(monitoringNamespace),
					crclient.MatchingLabels{"app": "control-plane-metrics-forwarder"},
				)).To(Succeed())
				g.Expect(podList.Items).NotTo(BeEmpty(), "control-plane-metrics-forwarder pod should exist in hosted cluster")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Waiting for Prometheus pod in hosted cluster")
			const promPodName = "prometheus-k8s-0"
			Eventually(func(g Gomega) {
				pod := &corev1.Pod{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: monitoringNamespace,
					Name:      promPodName,
				}, pod)).To(Succeed())
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), "prometheus pod should be running")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Verifying hosted cluster Prometheus is scraping kube-apiserver via the metrics-forwarder")
			Eventually(func(g Gomega) {
				output, err := v2util.RunCommandInPod(tc.Context, hcClientset, hcRestConfig,
					monitoringNamespace, promPodName, "prometheus",
					"curl", "-s", "http://localhost:9090/api/v1/targets")
				g.Expect(err).NotTo(HaveOccurred(), "should be able to query Prometheus targets API")
				g.Expect(output).To(ContainSubstring("control-plane-metrics-forwarder"),
					"Prometheus targets should include the metrics-forwarder scrape pool")
				g.Expect(output).To(ContainSubstring(`"health":"up"`),
					"metrics-forwarder target should be healthy")
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("Querying for actual kube-apiserver metrics scraped via the forwarder")
			Eventually(func(g Gomega) {
				output, err := v2util.RunCommandInPod(tc.Context, hcClientset, hcRestConfig,
					monitoringNamespace, promPodName, "prometheus",
					"curl", "-gs", `http://localhost:9090/api/v1/query?query=apiserver_request_total{job="apiserver"}`)
				g.Expect(err).NotTo(HaveOccurred(), "should be able to query Prometheus for apiserver_request_total")
				g.Expect(output).To(ContainSubstring(`"resultType":"vector"`),
					"Prometheus query should return vector results")
				g.Expect(output).NotTo(ContainSubstring(`"result":[]`),
					"should have apiserver_request_total metrics from kube-apiserver")
			}, 5*time.Minute, 15*time.Second).Should(Succeed())
		})
	})
}

func EnsureNodeTuningOperatorMetricsEndpointTest(getTestCtx internal.TestContextGetter) {
	When("cluster has worker nodes", func() {
		It("should have a functional node-tuning-operator metrics endpoint", func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(e2eutil.Version422)

			svc := &corev1.Service{}
			err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Name:      "node-tuning-operator",
				Namespace: tc.ControlPlaneNamespace,
			}, svc)
			if apierrors.IsNotFound(err) {
				Skip("node-tuning-operator service not found in control plane namespace, assuming no workers")
			}
			Expect(err).NotTo(HaveOccurred(), "failed to get node-tuning-operator service")

			Expect(svc.Spec.Ports).NotTo(BeEmpty(), "node-tuning-operator service should have at least one port")

			hasMetricsPort := false
			for _, port := range svc.Spec.Ports {
				if port.Name == "metrics" || port.Port == 60000 {
					hasMetricsPort = true
					break
				}
			}
			Expect(hasMetricsPort).To(BeTrue(), "node-tuning-operator service should expose a metrics port (named 'metrics' or on port 60000)")

			By("Validating ServiceMonitor exists with metrics endpoint")
			serviceMonitor := &monitoringv1.ServiceMonitor{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Name:      "node-tuning-operator",
				Namespace: tc.ControlPlaneNamespace,
			}, serviceMonitor)).To(Succeed(), "node-tuning-operator ServiceMonitor should exist")

			var targetPort string
			scheme := "https"
			for _, endpoint := range serviceMonitor.Spec.Endpoints {
				if endpoint.Path == "/metrics" {
					targetPort = endpoint.Port
					if targetPort == "" && endpoint.TargetPort != nil {
						targetPort = endpoint.TargetPort.String()
					}
					if endpoint.Scheme != nil {
						scheme = string(*endpoint.Scheme)
					}
					break
				}
			}
			Expect(targetPort).NotTo(BeEmpty(), "ServiceMonitor should have a /metrics endpoint with a target port")

			By("Verifying the HTTPS metrics endpoint returns Prometheus data")
			mgmtRestConfig, err := e2eutil.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "should be able to load management cluster REST config")
			clientset, err := kubernetes.NewForConfig(mgmtRestConfig)
			Expect(err).NotTo(HaveOccurred(), "should be able to create kubernetes clientset")

			httpsServiceURL := fmt.Sprintf("%s://node-tuning-operator.%s.svc.cluster.local:%s/metrics", scheme, tc.ControlPlaneNamespace, targetPort)
			Eventually(func(g Gomega) {
				ntoPods := &corev1.PodList{}
				g.Expect(tc.MgmtClient.List(tc.Context, ntoPods,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": "cluster-node-tuning-operator"},
				)).To(Succeed())
				g.Expect(ntoPods.Items).NotTo(BeEmpty(), "cluster-node-tuning-operator pod should exist")

				var runningPodName string
				for _, p := range ntoPods.Items {
					if p.Status.Phase == corev1.PodRunning {
						runningPodName = p.Name
						break
					}
				}
				g.Expect(runningPodName).NotTo(BeEmpty(), "a running cluster-node-tuning-operator pod should exist")

				output, err := v2util.RunCommandInPod(tc.Context, clientset, mgmtRestConfig,
					tc.ControlPlaneNamespace, runningPodName, "cluster-node-tuning-operator",
					"curl", "-s", "-f", "--max-time", "10",
					"--cacert", "/etc/secrets/ca.crt",
					"--cert", "/tmp/metrics-client-ca/tls.crt",
					"--key", "/tmp/metrics-client-ca/tls.key",
					httpsServiceURL)
				g.Expect(err).NotTo(HaveOccurred(), "should be able to curl NTO metrics endpoint at %s", httpsServiceURL)
				g.Expect(output).NotTo(BeEmpty(), "metrics response should not be empty")
				g.Expect(output).To(ContainSubstring("# HELP"),
					"metrics response should contain Prometheus format data")
			}, 3*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func EnsureKubeSchedulerMetricsEndpointTest(getTestCtx internal.TestContextGetter) {
	When("kube-scheduler is running", func() {
		It("should have functional kube-scheduler metrics endpoints", func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(e2eutil.Version423)

			// 1. Validate Service exists and has the "client" port
			svc := &corev1.Service{}
			err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Name:      "kube-scheduler",
				Namespace: tc.ControlPlaneNamespace,
			}, svc)
			Expect(err).NotTo(HaveOccurred(), "failed to get kube-scheduler service")
			Expect(svc.Spec.Ports).NotTo(BeEmpty(), "kube-scheduler service should have at least one port")

			var metricsPortNum int32
			hasClientPort := false
			for _, port := range svc.Spec.Ports {
				if port.Name == "client" || port.Port == 10259 {
					hasClientPort = true
					metricsPortNum = port.Port
					break
				}
			}
			Expect(hasClientPort).To(BeTrue(),
				"kube-scheduler service should expose a port named 'client' or on port 10259")

			// 2. Validate ServiceMonitor exists with both endpoints
			By("Validating ServiceMonitor exists with /metrics and /metrics/resources endpoints")
			serviceMonitor := &monitoringv1.ServiceMonitor{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Name:      "kube-scheduler",
				Namespace: tc.ControlPlaneNamespace,
			}, serviceMonitor)).To(Succeed(), "kube-scheduler ServiceMonitor should exist")

			foundDefaultMetrics := false
			foundResourceMetrics := false
			for _, ep := range serviceMonitor.Spec.Endpoints {
				if ep.Path == "" || ep.Path == "/metrics" {
					foundDefaultMetrics = true
				}
				if ep.Path == "/metrics/resources" {
					foundResourceMetrics = true
				}
			}
			Expect(foundDefaultMetrics).To(BeTrue(),
				"ServiceMonitor should have a /metrics endpoint")
			Expect(foundResourceMetrics).To(BeTrue(),
				"ServiceMonitor should have a /metrics/resources endpoint")

			// 3. Functional test - port-forward to the kube-scheduler pod and fetch metrics
			// using TLS certificates from the k8s API.
			By("Verifying the HTTPS metrics endpoints return Prometheus data")
			for _, metricsPath := range []string{"/metrics", "/metrics/resources"} {
				By(fmt.Sprintf("Testing kube-scheduler %s endpoint via port-forward", metricsPath))
				Eventually(func(g Gomega) {
					output, err := fetchMetricsViaPortForward(tc.Context, tc.MgmtClient,
						tc.ControlPlaneNamespace, "kube-scheduler", metricsPortNum, metricsPath, "kube-scheduler")
					g.Expect(err).NotTo(HaveOccurred(),
						"should be able to fetch kube-scheduler metrics at %s", metricsPath)
					g.Expect(output).NotTo(BeEmpty(),
						"metrics response should not be empty")
					g.Expect(output).To(ContainSubstring("# HELP"),
						"metrics response should contain Prometheus format data")
				}, 3*time.Minute, 10*time.Second).Should(Succeed())
			}
		})
	})
}

// fetchMetricsViaPortForward port-forwards to a running control plane pod selected by
// the "app=<componentLabel>" label in hcpNamespace and issues an mTLS GET against
// metricsPath on podPort. It authenticates with the "metrics-client" secret and trusts
// the "root-ca" ConfigMap, both read from hcpNamespace, and validates the server
// certificate against tlsServerName. It returns the response body on HTTP 200.
//
// It returns an error (rather than failing the test) so callers can retry inside
// Eventually(). Errors are returned when no running pod is found, the TLS material
// cannot be loaded, the port-forward cannot be established, or the endpoint returns a
// non-200 status.
func fetchMetricsViaPortForward(ctx context.Context, mgmtClient crclient.Client, hcpNamespace, componentLabel string, podPort int32, metricsPath, tlsServerName string) (string, error) {
	podList := &corev1.PodList{}
	if err := mgmtClient.List(ctx, podList, crclient.InNamespace(hcpNamespace), crclient.MatchingLabels{"app": componentLabel}); err != nil {
		return "", fmt.Errorf("failed to list %s pods: %w", componentLabel, err)
	}
	var runningPod *corev1.Pod
	for i := range podList.Items {
		if podList.Items[i].Status.Phase == corev1.PodRunning {
			runningPod = &podList.Items[i]
			break
		}
	}
	if runningPod == nil {
		return "", fmt.Errorf("no running %s pod found in namespace %s", componentLabel, hcpNamespace)
	}

	rootCA := &corev1.ConfigMap{}
	if err := mgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: "root-ca"}, rootCA); err != nil {
		return "", fmt.Errorf("failed to get root-ca ConfigMap: %w", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM([]byte(rootCA.Data["ca.crt"])) {
		return "", fmt.Errorf("failed to parse root-ca certificate")
	}

	metricsClientSecret := &corev1.Secret{}
	if err := mgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: "metrics-client"}, metricsClientSecret); err != nil {
		return "", fmt.Errorf("failed to get metrics-client secret: %w", err)
	}
	clientCert, err := tls.X509KeyPair(metricsClientSecret.Data["tls.crt"], metricsClientSecret.Data["tls.key"])
	if err != nil {
		return "", fmt.Errorf("failed to parse metrics-client certificate: %w", err)
	}

	restConfig, err := e2eutil.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get rest config: %w", err)
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "localhost:0")
	if err != nil {
		return "", fmt.Errorf("failed to allocate local port: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	stopChan := make(chan struct{})
	defer close(stopChan)
	fwd := &supportforwarder.PortForwarder{
		Namespace: hcpNamespace,
		PodName:   runningPod.Name,
		Client:    kubeClient,
		Config:    restConfig,
		Out:       io.Discard,
		ErrOut:    io.Discard,
	}
	if err := fwd.ForwardPorts([]string{fmt.Sprintf("%d:%d", localPort, podPort)}, stopChan); err != nil {
		return "", fmt.Errorf("failed to start port-forward: %w", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      certPool,
				Certificates: []tls.Certificate{clientCert},
				ServerName:   tlsServerName,
				MinVersion:   tls.VersionTLS12,
			},
		},
		Timeout: 10 * time.Second,
	}

	url := fmt.Sprintf("https://localhost:%d%s", localPort, metricsPath)
	metricsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for %s: %w", url, err)
	}
	resp, err := httpClient.Do(metricsReq)
	if err != nil {
		return "", fmt.Errorf("failed to GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	return string(body), nil
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Metrics] Hosted Cluster Metrics", Label("hosted-cluster-metrics"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterMetricsTests(func() *internal.TestContext { return testCtx })
})
