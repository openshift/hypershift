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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"
	azureutil "github.com/openshift/hypershift/support/azureutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterMetricsTests(getTestCtx internal.TestContextGetter) {
	ValidateMetricsTest(getTestCtx)
	EnsureMetricsForwarderWorkingTest(getTestCtx)
	EnsureNodeTuningOperatorMetricsEndpointTest(getTestCtx)
}

func ValidateMetricsTest(getTestCtx internal.TestContextGetter) {
	When("HyperShift operator is running", func() {
		It("should expose expected metrics at the metrics endpoint", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()
			if hostedCluster.Spec.Platform.Type == hyperv1.NonePlatform {
				Skip("metrics test skipped for None platform")
			}

			mgmtRestConfig, err := e2eutil.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "should be able to load management cluster REST config")

			clientset, err := kubernetes.NewForConfig(mgmtRestConfig)
			Expect(err).NotTo(HaveOccurred(), "should be able to create kubernetes clientset")

			hoNamespace := "hypershift"
			podList := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, podList,
				crclient.InNamespace(hoNamespace),
				crclient.MatchingLabels{"app": "operator"},
			)).To(Succeed(), "should be able to list pods in the hypershift namespace")

			if len(podList.Items) == 0 {
				Skip("hypershift-operator pod not found in the hypershift namespace")
			}

			hoPodName := podList.Items[0].Name
			hcName := hostedCluster.Name

			Eventually(func(g Gomega) {
				metrics, err := v2util.GetMetricsFromPod(tc.Context, clientset, mgmtRestConfig, hoNamespace, hoPodName, "operator", 9000)
				g.Expect(err).NotTo(HaveOccurred(), "should be able to fetch metrics from hypershift-operator pod")

				g.Expect(metrics).To(HaveKey("hypershift_operator_info"),
					"metrics should contain hypershift_operator_info")

				for _, metricName := range []string{
					hcmetrics.SilenceAlertsMetricName,
					hcmetrics.LimitedSupportEnabledMetricName,
					hcmetrics.ProxyMetricName,
				} {
					family, ok := metrics[metricName]
					g.Expect(ok).To(BeTrue(), "metric %s should exist", metricName)
					hasMatch := false
					for _, m := range family.Metric {
						for _, l := range m.GetLabel() {
							if l.GetName() == "name" && l.GetValue() == hcName {
								hasMatch = true
							}
						}
					}
					g.Expect(hasMatch).To(BeTrue(),
						"metric %s should have label name=%s", metricName, hcName)
				}

				for _, metricName := range []string{
					npmetrics.SizeMetricName,
					npmetrics.AvailableReplicasMetricName,
				} {
					family, ok := metrics[metricName]
					g.Expect(ok).To(BeTrue(), "metric %s should exist", metricName)
					hasMatch := false
					for _, m := range family.Metric {
						for _, l := range m.GetLabel() {
							if l.GetName() == "cluster_name" && l.GetValue() == hcName {
								hasMatch = true
							}
						}
					}
					g.Expect(hasMatch).To(BeTrue(),
						"metric %s should have label cluster_name=%s", metricName, hcName)
				}

				if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
					family, ok := metrics[hcmetrics.InvalidAwsCredsMetricName]
					g.Expect(ok).To(BeTrue(), "metric %s should exist on AWS", hcmetrics.InvalidAwsCredsMetricName)
					hasMatch := false
					for _, m := range family.Metric {
						for _, l := range m.GetLabel() {
							if l.GetName() == "name" && l.GetValue() == hcName {
								hasMatch = true
							}
						}
					}
					g.Expect(hasMatch).To(BeTrue(),
						"metric %s should have label name=%s", hcmetrics.InvalidAwsCredsMetricName, hcName)
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
			hostedCluster := tc.GetHostedCluster()
			if e2eutil.IsLessThan(e2eutil.Version422) {
				Skip("metrics forwarder requires version >= 4.22")
			}

			if hostedCluster.Annotations[hyperv1.EnableMetricsForwarding] != "true" {
				Skip("metrics forwarding annotation not set on hosted cluster; skipping verification test")
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

			By("Waiting for guest-side metrics-forwarder deployment")
			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()
			hcRestConfig := tc.GetHostedClusterRESTConfig()
			Expect(hcRestConfig).NotTo(BeNil(), "hosted cluster REST config should be available")

			hcClientset, err := kubernetes.NewForConfig(hcRestConfig)
			Expect(err).NotTo(HaveOccurred(), "should be able to create hosted cluster kubernetes clientset")

			const monitoringNamespace = "openshift-monitoring"
			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(hcClient.List(tc.Context, podList,
					crclient.InNamespace(monitoringNamespace),
					crclient.MatchingLabels{"app": "control-plane-metrics-forwarder"},
				)).To(Succeed())
				g.Expect(podList.Items).NotTo(BeEmpty(), "control-plane-metrics-forwarder pod should exist in guest cluster")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Waiting for Prometheus pod in guest cluster")
			const promPodName = "prometheus-k8s-0"
			Eventually(func(g Gomega) {
				pod := &corev1.Pod{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: monitoringNamespace,
					Name:      promPodName,
				}, pod)).To(Succeed())
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), "prometheus pod should be running")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Verifying guest Prometheus is scraping kube-apiserver via the metrics-forwarder")
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
			output, err := v2util.RunCommandInPod(tc.Context, hcClientset, hcRestConfig,
				monitoringNamespace, promPodName, "prometheus",
				"curl", "-gs", `http://localhost:9090/api/v1/query?query=apiserver_request_total{job="apiserver"}`)
			Expect(err).NotTo(HaveOccurred(), "should be able to query Prometheus for apiserver_request_total")
			Expect(output).To(ContainSubstring(`"resultType":"vector"`),
				"Prometheus query should return vector results")
			Expect(output).NotTo(ContainSubstring(`"result":[]`),
				"should have apiserver_request_total metrics from kube-apiserver")
		})
	})
}

func EnsureNodeTuningOperatorMetricsEndpointTest(getTestCtx internal.TestContextGetter) {
	When("cluster has worker nodes", func() {
		It("should have a functional node-tuning-operator metrics endpoint", func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version422) {
				Skip("NTO metrics endpoint test requires version >= 4.22")
			}

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
					targetPort = endpoint.TargetPort.String()
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

			ntoPods := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, ntoPods,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{"app": "cluster-node-tuning-operator"},
			)).To(Succeed())
			Expect(ntoPods.Items).NotTo(BeEmpty(), "cluster-node-tuning-operator pod should exist")

			httpsServiceURL := fmt.Sprintf("%s://node-tuning-operator.%s.svc.cluster.local:%s/metrics", scheme, tc.ControlPlaneNamespace, targetPort)
			Eventually(func(g Gomega) {
				output, err := v2util.RunCommandInPod(tc.Context, clientset, mgmtRestConfig,
					tc.ControlPlaneNamespace, ntoPods.Items[0].Name, "cluster-node-tuning-operator",
					"curl", "-s", "-f", "--max-time", "10",
					"--cacert", "/etc/secrets/ca.crt",
					"--cert", "/tmp/metrics-client-ca/tls.crt",
					"--key", "/tmp/metrics-client-ca/tls.key",
					httpsServiceURL)
				g.Expect(err).NotTo(HaveOccurred(), "should be able to curl NTO metrics endpoint at %s", httpsServiceURL)
				g.Expect(output).NotTo(BeEmpty(), "metrics response should not be empty")
				g.Expect(strings.Contains(output, "# HELP")).To(BeTrue(),
					"metrics response should contain Prometheus format data")
			}, 3*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("Hosted Cluster Metrics", Label("hosted-cluster-metrics"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterMetricsTests(func() *internal.TestContext { return testCtx })
})
