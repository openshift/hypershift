//go:build e2ev2

package tests

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	testutil "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	gcpIngressWorkloadName = "gcp-ingress-workload"
	gcpIngressWorkloadPort = 8080

	// gcpIngressWorkloadImage ships python3 (for a trivial HTTP server) and
	// curl (for the in-cluster route probe), so the test needs no package
	// installs on the worker nodes. It is the same image the GCP worker
	// firewall probe uses.
	gcpIngressWorkloadImage = "registry.access.redhat.com/ubi9/ubi:latest"
)

func gcpIngressWorkloadLabels() map[string]string {
	return map[string]string{"app": gcpIngressWorkloadName}
}

func buildGCPIngressWorkloadDeployment(namespace string) *appsv1.Deployment {
	labels := gcpIngressWorkloadLabels()
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: gcpIngressWorkloadName, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "workload",
							Image:   gcpIngressWorkloadImage,
							Command: []string{"python3", "-m", "http.server", strconv.Itoa(gcpIngressWorkloadPort)},
							Ports:   []corev1.ContainerPort{{Name: "http", ContainerPort: gcpIngressWorkloadPort}},
						},
					},
				},
			},
		},
	}
}

func buildGCPIngressWorkloadService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: gcpIngressWorkloadName, Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: gcpIngressWorkloadLabels(),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       gcpIngressWorkloadPort,
					TargetPort: intstr.FromString("http"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

func buildGCPIngressWorkloadRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: gcpIngressWorkloadName, Namespace: namespace},
		Spec: routev1.RouteSpec{
			To:   routev1.RouteTargetReference{Kind: "Service", Name: gcpIngressWorkloadName},
			Port: &routev1.RoutePort{TargetPort: intstr.FromString("http")},
		},
	}
}

// routeAdmittedHost returns the admitted host of the route, or "" when the
// ingress controller has not admitted it yet.
func routeAdmittedHost(route *routev1.Route) string {
	for _, ingress := range route.Status.Ingress {
		for _, condition := range ingress.Conditions {
			if condition.Type == routev1.RouteAdmitted && condition.Status == corev1.ConditionTrue {
				return ingress.Host
			}
		}
	}
	return ""
}

// GCPIngressTests validates the GCP ingress path on a cluster that runs with
// the Ingress capability enabled: the guest dns.config must point at the
// CPO-managed ingress zones, and the guest ingress operator must serve a
// workload through the default ingress controller.
func GCPIngressTests(getTestCtx internal.TestContextGetter) {
	When("the hosted cluster is on GCP with the Ingress capability enabled", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.GCPPlatform)

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			if !capabilities.IsIngressCapabilityEnabled(hc.Spec.Capabilities) {
				Skip("Ingress capability is disabled on the HostedCluster")
			}
		})

		It("should point the guest dns.config at the CPO-managed ingress zones", Label(internal.InformingLabel), func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

			// The ingress operator creates the *.apps wildcard in the zones
			// named here, so no record is published unless both are set.
			Eventually(func(g Gomega) {
				dnsConfig := &configv1.DNS{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Name: "cluster"}, dnsConfig)).To(Succeed())
				g.Expect(dnsConfig.Spec.PublicZone).NotTo(BeNil(), "guest dns.config should reference the public ingress zone")
				g.Expect(dnsConfig.Spec.PublicZone.ID).NotTo(BeEmpty(), "guest dns.config public ingress zone ID should be set")
				g.Expect(dnsConfig.Spec.PrivateZone).NotTo(BeNil(), "guest dns.config should reference the private ingress zone")
				g.Expect(dnsConfig.Spec.PrivateZone.ID).NotTo(BeEmpty(), "guest dns.config private ingress zone ID should be set")
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
		})

		It("should serve a workload through the default ingress controller", Label(internal.InformingLabel, "lifecycle"), func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			ingressDomain := ingressDomainForHostedCluster(hc)

			restConfig, err := tc.GetHostedClusterRESTConfig(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster REST config")
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")
			clientset, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create hosted cluster clientset")

			By("Waiting for the default IngressController to be available on " + ingressDomain)
			Eventually(func(g Gomega) {
				ic := &operatorv1.IngressController{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: "openshift-ingress-operator",
					Name:      "default",
				}, ic)).To(Succeed())
				var available *operatorv1.OperatorCondition
				for i := range ic.Status.Conditions {
					if ic.Status.Conditions[i].Type == operatorv1.IngressControllerAvailableConditionType {
						available = &ic.Status.Conditions[i]
						break
					}
				}
				g.Expect(available).NotTo(BeNil(), "default IngressController should report an Available condition")
				g.Expect(available.Status).To(Equal(operatorv1.ConditionTrue),
					"IngressController should be Available, got %s (%s: %s)", available.Status, available.Reason, available.Message)
				g.Expect(ic.Status.Domain).To(Equal(ingressDomain),
					"IngressController domain should be the cluster ingress domain")
			}, 20*time.Minute, 15*time.Second).Should(Succeed())

			// Use a generated namespace so this test only ever owns (and
			// deletes) a namespace it created, never one that pre-exists.
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "hcp-e2e-gcp-ingress-"}}
			Expect(hcClient.Create(tc.Context, namespace)).To(Succeed(), "failed to create test namespace")
			ns := namespace.Name
			DeferCleanup(func() {
				if err := hcClient.Delete(tc.Context, namespace); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete namespace %s", ns)
				}
			})

			deployment := buildGCPIngressWorkloadDeployment(ns)
			Expect(hcClient.Create(tc.Context, deployment)).To(Succeed(), "failed to create workload deployment")
			DeferCleanup(func() {
				if err := hcClient.Delete(tc.Context, deployment); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete workload deployment")
				}
			})

			service := buildGCPIngressWorkloadService(ns)
			Expect(hcClient.Create(tc.Context, service)).To(Succeed(), "failed to create workload service")
			DeferCleanup(func() {
				if err := hcClient.Delete(tc.Context, service); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete workload service")
				}
			})

			route := buildGCPIngressWorkloadRoute(ns)
			Expect(hcClient.Create(tc.Context, route)).To(Succeed(), "failed to create workload route")
			DeferCleanup(func() {
				if err := hcClient.Delete(tc.Context, route); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete workload route")
				}
			})

			By("Waiting for the ingress controller to admit the route under the ingress domain")
			var routeHost string
			Eventually(func(g Gomega) {
				current := &routev1.Route{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(route), current)).To(Succeed())
				host := routeAdmittedHost(current)
				g.Expect(host).NotTo(BeEmpty(), "route %s/%s should be admitted", ns, gcpIngressWorkloadName)
				g.Expect(strings.HasSuffix(host, "."+ingressDomain)).To(BeTrue(),
					"admitted route host %q should be under the ingress domain %q", host, ingressDomain)
				routeHost = host
			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			By("Waiting for the workload pod to be running")
			var podName string
			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(hcClient.List(tc.Context, podList,
					crclient.InNamespace(ns),
					crclient.MatchingLabels(gcpIngressWorkloadLabels()),
				)).To(Succeed())
				for _, pod := range podList.Items {
					if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" && pod.DeletionTimestamp.IsZero() {
						podName = pod.Name
						return
					}
				}
				podName = ""
				g.Expect(podName).NotTo(BeEmpty(), "expected a running workload pod in namespace %s", ns)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			// The request runs from inside the hosted cluster: the route host
			// must resolve through the ingress zones and reach the router that
			// terminates it and forwards to the workload service.
			url := fmt.Sprintf("http://%s/", routeHost)
			By("Requesting " + url + " from inside the hosted cluster")
			Eventually(func(g Gomega) {
				out, err := testutil.RunCommandInPod(tc.Context, clientset, restConfig, ns, podName, "workload",
					"curl", "-sS", "-m", "10", "-o", "/dev/null", "-w", "%{http_code}", url)
				g.Expect(err).NotTo(HaveOccurred(), "request through the ingress controller should succeed for %s", url)
				g.Expect(out).To(Equal("200"), "unexpected HTTP status through the ingress controller for %s", url)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GCPIngress] GCP Ingress", Label("gcp-ingress"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	GCPIngressTests(func() *internal.TestContext { return testCtx })
})
