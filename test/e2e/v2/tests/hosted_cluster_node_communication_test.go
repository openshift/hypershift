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
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	testutil "github.com/openshift/hypershift/test/e2e/v2/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterNodeCommunicationTests(getTestCtx internal.TestContextGetter) {
	EnsureNodeCommunicationTest(getTestCtx)
	EnsureGCPWorkerFirewallTest(getTestCtx)
}

func EnsureNodeCommunicationTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has konnectivity tunnel configured", func() {
		It("should have konnectivity-agent pods with retrievable logs", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			restConfig, err := tc.GetHostedClusterRESTConfig(hc)
			Expect(err).NotTo(HaveOccurred())

			clientset, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create hosted cluster kubernetes clientset")

			Eventually(func(g Gomega) {
				podList, err := clientset.CoreV1().Pods("kube-system").List(tc.Context, metav1.ListOptions{
					LabelSelector: "app=konnectivity-agent",
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list konnectivity-agent pods")
				g.Expect(podList.Items).NotTo(BeEmpty(), "expected at least one konnectivity-agent pod in kube-system")

				retrieved := false
				for _, pod := range podList.Items {
					_, err = clientset.CoreV1().Pods("kube-system").GetLogs(pod.Name, &corev1.PodLogOptions{
						Container: "konnectivity-agent",
					}).DoRaw(tc.Context)
					if err == nil {
						retrieved = true
						break
					}
				}
				g.Expect(retrieved).To(BeTrue(), "failed to retrieve logs from any konnectivity-agent pod")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// gcpFirewallProbePort is the container/service port used by the connectivity
// probe workload. It falls inside the NodePort service range (30000-32767) that
// the CPO-managed worker firewall rule (<infra-id>-internal-cluster) allows, so
// the NodePort probe also exercises that allowance.
const gcpFirewallProbePort = 31251

// gcpFirewallProbeImage is a UBI image that ships both python3 (for a trivial
// HTTP server) and curl (for the client probe), avoiding any extra package
// installs on the worker nodes.
const gcpFirewallProbeImage = "registry.access.redhat.com/ubi9/ubi:latest"

// EnsureGCPWorkerFirewallTest validates that the CPO-managed GCP worker firewall
// rule permits the worker-to-worker traffic it is responsible for. It is a
// no-op on non-GCP platforms.
//
// It verifies:
//  1. The GCPFirewallRulesReady condition on the HostedCluster is True (the CPO
//     reconciled the managed rule to its desired state).
//  2. Cross-node pod-to-pod traffic works: a pod on one worker reaches a pod on
//     a different worker over its pod IP, which rides the OVN-Kubernetes Geneve
//     overlay (UDP 6081) that the managed rule allows only for OVNKubernetes.
//  3. NodePort traffic works: a pod reaches the probe workload via a NodePort on
//     a different worker's internal IP, exercising the 30000-32767 allowance.
func EnsureGCPWorkerFirewallTest(getTestCtx internal.TestContextGetter) {
	When("the hosted cluster is on GCP", func() {
		BeforeEach(func() {
			getTestCtx().SkipIfNotPlatform(hyperv1.GCPPlatform)
		})

		It("should reconcile the worker firewall rule and allow cross-node worker traffic", Label("lifecycle", "gcp-worker-firewall"), func() {
			tc := getTestCtx()

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			// 1. The CPO must converge the managed firewall rule. This is mirrored
			//    from the HostedControlPlane onto the HostedCluster.
			Eventually(func(g Gomega) {
				hc, err := tc.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.GCPFirewallRulesReady))
				g.Expect(cond).NotTo(BeNil(), "expected GCPFirewallRulesReady condition on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
					"GCPFirewallRulesReady should be True, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			restConfig, err := tc.GetHostedClusterRESTConfig(hc)
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			clientset, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create hosted cluster kubernetes clientset")

			// Require at least two schedulable, Ready worker nodes for a
			// cross-node test. Counting all node entries would include cordoned
			// or NotReady nodes that can't run the probe workload.
			nodeList := &corev1.NodeList{}
			Expect(hcClient.List(tc.Context, nodeList)).To(Succeed())
			eligibleNodes := 0
			for i := range nodeList.Items {
				if isNodeSchedulableAndReady(&nodeList.Items[i]) {
					eligibleNodes++
				}
			}
			if eligibleNodes < 2 {
				Skip(fmt.Sprintf("cross-node firewall test requires >=2 schedulable Ready worker nodes, got %d", eligibleNodes))
			}

			// Use a generated namespace name so this test only ever owns (and
			// deletes) a namespace it created, never one that pre-exists.
			ns := e2eutil.SimpleNameGenerator.GenerateName("hcp-e2e-gcp-firewall-")
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
			Expect(hcClient.Create(tc.Context, namespace)).To(Succeed(), "failed to create test namespace %s", ns)
			DeferCleanup(func() {
				err := hcClient.Delete(tc.Context, namespace)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete namespace %s", ns)
				}
			})

			// Deploy a 2-replica probe app with anti-affinity so replicas land on
			// distinct worker nodes.
			deployment := buildGCPFirewallProbeDeployment(ns)
			Expect(hcClient.Create(tc.Context, deployment)).To(Succeed(), "failed to create probe deployment")
			DeferCleanup(func() {
				err := hcClient.Delete(tc.Context, deployment)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete probe deployment")
				}
			})

			nodePortService := buildGCPFirewallProbeService(ns)
			Expect(hcClient.Create(tc.Context, nodePortService)).To(Succeed(), "failed to create probe NodePort service")
			DeferCleanup(func() {
				err := hcClient.Delete(tc.Context, nodePortService)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete probe service")
				}
			})

			// Wait for both replicas to be running on different nodes.
			var probePods []corev1.Pod
			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(hcClient.List(tc.Context, podList,
					crclient.InNamespace(ns),
					crclient.MatchingLabels{"app": "hcp-e2e-gcp-firewall"},
				)).To(Succeed())

				running := runningPods(podList.Items)
				g.Expect(running).To(HaveLen(2), "expected 2 running probe pods, got %d", len(running))

				nodesByPod := map[string]string{}
				for _, p := range running {
					g.Expect(p.Status.PodIP).NotTo(BeEmpty(), "pod %s has no pod IP yet", p.Name)
					nodesByPod[p.Name] = p.Spec.NodeName
				}
				nodes := map[string]struct{}{}
				for _, node := range nodesByPod {
					nodes[node] = struct{}{}
				}
				g.Expect(nodes).To(HaveLen(2), "probe pods did not spread across 2 distinct nodes: %v", nodesByPod)
				probePods = running
			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			source := probePods[0]
			target := probePods[1]
			targetNodeIP := internalIPForNode(nodeList, target.Spec.NodeName)
			Expect(targetNodeIP).NotTo(BeEmpty(), "no internal IP found for node %s", target.Spec.NodeName)

			probePort := fmt.Sprintf("%d", gcpFirewallProbePort)

			// 2. Cross-node pod-to-pod over the pod IP (Geneve overlay / UDP 6081).
			podURL := "http://" + net.JoinHostPort(target.Status.PodIP, probePort) + "/"
			Eventually(func(g Gomega) {
				out, err := testutil.RunCommandInPod(tc.Context, clientset, restConfig, ns, source.Name, "probe",
					"curl", "-sS", "-m", "10", "-o", "/dev/null", "-w", "%{http_code}", podURL)
				g.Expect(err).NotTo(HaveOccurred(), "cross-node pod-to-pod curl failed")
				g.Expect(out).To(Equal("200"), "unexpected HTTP status from cross-node pod IP probe")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			// 3. NodePort traffic to a different node's internal IP. Read the
			//    Kubernetes-allocated NodePort back from the created Service.
			Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(nodePortService), nodePortService)).To(Succeed())
			Expect(nodePortService.Spec.Ports).NotTo(BeEmpty(), "probe service has no ports")
			allocatedNodePort := nodePortService.Spec.Ports[0].NodePort
			Expect(allocatedNodePort).NotTo(BeZero(), "probe service did not get a NodePort allocated")
			nodePortURL := "http://" + net.JoinHostPort(targetNodeIP, fmt.Sprintf("%d", allocatedNodePort)) + "/"
			Eventually(func(g Gomega) {
				out, err := testutil.RunCommandInPod(tc.Context, clientset, restConfig, ns, source.Name, "probe",
					"curl", "-sS", "-m", "10", "-o", "/dev/null", "-w", "%{http_code}", nodePortURL)
				g.Expect(err).NotTo(HaveOccurred(), "cross-node NodePort curl failed")
				g.Expect(out).To(Equal("200"), "unexpected HTTP status from cross-node NodePort probe")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// runningPods returns the subset of pods that are in the Running phase.
func runningPods(pods []corev1.Pod) []corev1.Pod {
	var running []corev1.Pod
	for i := range pods {
		if pods[i].Status.Phase == corev1.PodRunning {
			running = append(running, pods[i])
		}
	}
	return running
}

// isNodeSchedulableAndReady reports whether a node is uncordoned and reporting
// Ready, i.e. eligible to run the cross-node probe workload.
func isNodeSchedulableAndReady(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// internalIPForNode returns the InternalIP address of the named node, or "" if
// not found.
func internalIPForNode(nodeList *corev1.NodeList, nodeName string) string {
	for i := range nodeList.Items {
		if nodeList.Items[i].Name != nodeName {
			continue
		}
		for _, addr := range nodeList.Items[i].Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address
			}
		}
	}
	return ""
}

// buildGCPFirewallProbeDeployment builds a 2-replica Deployment that serves a
// trivial HTTP endpoint on gcpFirewallProbePort. Anti-affinity spreads the
// replicas across distinct worker nodes.
func buildGCPFirewallProbeDeployment(namespace string) *appsv1.Deployment {
	labels := map[string]string{"app": "hcp-e2e-gcp-firewall"}
	name := e2eutil.SimpleNameGenerator.GenerateName("gcp-fw-probe-")
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](2),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
								LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
								TopologyKey:   "kubernetes.io/hostname",
							}},
						},
					},
					Containers: []corev1.Container{{
						Name:    "probe",
						Image:   gcpFirewallProbeImage,
						Command: []string{"/usr/bin/python3", "-m", "http.server", fmt.Sprintf("%d", gcpFirewallProbePort)},
						Ports:   []corev1.ContainerPort{{ContainerPort: gcpFirewallProbePort}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(gcpFirewallProbePort)},
							},
						},
					}},
					TerminationGracePeriodSeconds: ptr.To[int64](5),
				},
			},
		},
	}
}

// buildGCPFirewallProbeService exposes the probe workload via a NodePort so
// cross-node NodePort traffic can be validated. The NodePort is left unset so
// Kubernetes allocates a free port from its NodePort range (default
// 30000-32767, which the managed firewall rule allows); pinning a fixed port
// risks a collision that the API server would reject.
func buildGCPFirewallProbeService(namespace string) *corev1.Service {
	labels := map[string]string{"app": "hcp-e2e-gcp-firewall"}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "gcp-fw-probe", Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       gcpFirewallProbePort,
				TargetPort: intstr.FromInt(gcpFirewallProbePort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodeCommunication] Hosted Cluster Node Communication", Label("hosted-cluster-node-communication"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNodeCommunicationTests(func() *internal.TestContext { return testCtx })
})
