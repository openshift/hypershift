//go:build e2ev2

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func RegisterHostedClusterMetricsLifecycleTests(getTestCtx internal.TestContextGetter) {
	It("should forward All metrics and filter non-telemetry series after switching to Telemetry", func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.22.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		original := hc.Spec.Monitoring.MetricsForwarding
		DeferCleanup(func() {
			Eventually(func(g Gomega) {
				current := &hyperv1.HostedCluster{}
				err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)
				if apierrors.IsNotFound(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred())
				current.Spec.Monitoring.MetricsForwarding = original
				g.Expect(tc.MgmtClient.Update(tc.Context, current)).To(Succeed())
			}, time.Minute, 5*time.Second).Should(Succeed())
		})
		hostedClient, err := tc.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		restConfig, err := tc.GetHostedClusterRESTConfig(hc)
		Expect(err).NotTo(HaveOccurred())
		clientset, err := kubernetes.NewForConfig(restConfig)
		Expect(err).NotTo(HaveOccurred())
		waitDeployment := func(client crclient.Client, namespace, name string) {
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				g.Expect(client.Get(tc.Context, crclient.ObjectKey{Namespace: namespace, Name: name}, deployment)).To(Succeed())
				g.Expect(metricsDeploymentReady(deployment)).To(BeTrue(), "deployment %s/%s must finish rolling out", namespace, name)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		}
		queryMetric := func(query string, present bool, timeout time.Duration) {
			Eventually(func(g Gomega) {
				output, err := v2util.RunCommandInPod(tc.Context, clientset, restConfig, "openshift-monitoring", "prometheus-k8s-0", "prometheus", "curl", "-fgs", "http://localhost:9090/api/v1/query?query="+query)
				g.Expect(err).NotTo(HaveOccurred())
				count, err := metricsVectorSize(output)
				g.Expect(err).NotTo(HaveOccurred())
				if present {
					g.Expect(count).To(BeNumerically(">", 0))
				} else {
					g.Expect(count).To(BeZero())
				}
			}, timeout, 15*time.Second).Should(Succeed())
		}
		for _, metricsSet := range []hyperv1.MetricsSet{hyperv1.MetricsSetAll, hyperv1.MetricsSetTelemetry} {
			Eventually(func(g Gomega) {
				current := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)).To(Succeed())
				current.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				current.Spec.Monitoring.MetricsForwarding.MetricsSet = metricsSet
				g.Expect(tc.MgmtClient.Update(tc.Context, current)).To(Succeed())
			}, time.Minute, 5*time.Second).Should(Succeed())
			for _, name := range []string{"endpoint-resolver", "metrics-proxy"} {
				waitDeployment(tc.MgmtClient, tc.ControlPlaneNamespace, name)
			}
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "metrics-proxy"}, deployment)).To(Succeed())
				found := false
				for _, container := range deployment.Spec.Template.Spec.Containers {
					if container.Name != "metrics-proxy" {
						continue
					}
					for index, argument := range container.Args {
						if argument == "--metrics-set" && index+1 < len(container.Args) && container.Args[index+1] == string(metricsSet) {
							found = true
						}
					}
				}
				g.Expect(found).To(BeTrue(), "metrics-proxy must select %s", metricsSet)
				g.Expect(metricsDeploymentReady(deployment)).To(BeTrue())
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
			waitDeployment(hostedClient, "openshift-monitoring", "control-plane-metrics-forwarder")
			Eventually(func(g Gomega) {
				pod := &corev1.Pod{}
				g.Expect(hostedClient.Get(tc.Context, crclient.ObjectKey{Namespace: "openshift-monitoring", Name: "prometheus-k8s-0"}, pod)).To(Succeed())
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				output, err := v2util.RunCommandInPod(tc.Context, clientset, restConfig, "openshift-monitoring", "prometheus-k8s-0", "prometheus", "curl", "-fs", "http://localhost:9090/api/v1/targets")
				g.Expect(err).NotTo(HaveOccurred())
				up, err := metricsForwarderTargetUp(output)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(up).To(BeTrue())
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
			queryMetric(`apiserver_request_total{job="apiserver"}`, true, 5*time.Minute)
			queryMetric(`process_resident_memory_bytes{job="apiserver"}`, metricsSet == hyperv1.MetricsSetAll, 8*time.Minute)
		}
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Metrics] Hosted Cluster Metrics Lifecycle", Label("hosted-cluster-metrics-lifecycle"), Serial, func() {
	RegisterHostedClusterMetricsLifecycleTests(internal.GetTestContext)
})
