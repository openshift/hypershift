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

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/support/netutil"

	configv1 "github.com/openshift/api/config/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterSecurityTests(getTestCtx internal.TestContextGetter) {
	EnsureHostedClusterWebhooksValidatedTest(getTestCtx)
	EnsureAdmissionPoliciesTest(getTestCtx)
	EnsureNetworkPoliciesTest(getTestCtx)
}

func EnsureHostedClusterWebhooksValidatedTest(getTestCtx internal.TestContextGetter) {
	When("[Feature:WebhookValidation] a webhook targeting a control plane service is created in the hosted cluster", func() {
		It("should be automatically deleted", Label("hosted-cluster-webhook-validation"), func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			sideEffectsNone := admissionregistrationv1.SideEffectClassNone
			webhookConf := &admissionregistrationv1.ValidatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-malicious-webhook",
					Annotations: map[string]string{
						"service.beta.openshift.io/inject-cabundle": "true",
					},
				},
				Webhooks: []admissionregistrationv1.ValidatingWebhook{{
					AdmissionReviewVersions: []string{"v1"},
					Name:                    "etcd-client.example.com",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						URL: ptr.To("https://etcd-client:2379"),
					},
					Rules: []admissionregistrationv1.RuleWithOperations{{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					}},
					SideEffects: &sideEffectsNone,
				}},
			}

			Expect(hcClient.Create(tc.Context, webhookConf)).To(Succeed())
			DeferCleanup(func() {
				err := hcClient.Delete(tc.Context, webhookConf)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "failed to cleanup test-malicious-webhook — this webhook may disrupt the hosted cluster")
				}
			})

			Eventually(func(g Gomega) {
				existing := &admissionregistrationv1.ValidatingWebhookConfiguration{}
				err := hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(webhookConf), existing)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "webhook should have been deleted by HCCO")
			}, 3*time.Minute, 5*time.Second).Should(Succeed())
		})
	})
}

func EnsureAdmissionPoliciesTest(getTestCtx internal.TestContextGetter) {
	When("[Feature:AdmissionPolicies] checking admission policies on a public hosted cluster", Label("hosted-cluster-admission-policies"), func() {
		var tc *internal.TestContext

		BeforeEach(func() {
			tc = getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !netutil.IsPublicHC(hc) {
				Skip("admission policies test requires a public hosted cluster")
			}
		})

		It("should find all required ValidatingAdmissionPolicies", func() {
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			eligible, err := securityAdmissionAtLeast(tc, hc, semver.MustParse("4.18.0"))
			Expect(err).NotTo(HaveOccurred())
			if !eligible {
				Skip("admission policies require control plane release 4.18 or later")
			}
			requiredVAPs, err := securityRequiredAdmissionPolicies(tc)
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				vapList := &admissionregistrationv1.ValidatingAdmissionPolicyList{}
				g.Expect(hcClient.List(tc.Context, vapList)).To(Succeed())
				g.Expect(vapList.Items).NotTo(BeEmpty(), "expected ValidatingAdmissionPolicies to be present")

				vapNames := make([]string, 0, len(vapList.Items))
				for _, vap := range vapList.Items {
					vapNames = append(vapNames, vap.Name)
				}
				for _, required := range requiredVAPs {
					g.Expect(vapNames).To(ContainElement(required),
						"required ValidatingAdmissionPolicy %s not found", required)
				}
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should deny unauthorized config changes via VAPs", func() {
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			eligible, err := securityAdmissionAtLeast(tc, hc, semver.MustParse("4.18.0"))
			Expect(err).NotTo(HaveOccurred())
			if !eligible {
				Skip("admission policies require control plane release 4.18 or later")
			}
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				apiServer := &configv1.APIServer{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, apiServer)).To(Succeed())
				g.Expect(securityDeniedAuditChange(tc.Context, hcClient, apiServer)).To(Succeed())
			}, time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should deny unauthorized deletion of kas-bootstrap RBAC bindings via VAPs", func() {
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			eligible, err := securityAdmissionAtLeast(tc, hc, semver.MustParse("5.1.0"))
			Expect(err).NotTo(HaveOccurred())
			if !eligible {
				Skip("protected RBAC admission policy requires control plane release 5.1 or later")
			}
			// The admin kubeconfig authenticates as system:admin, which the policy whitelists so
			// the KAS bootstrap container can apply these bindings. Impersonate an unrelated user
			// to exercise the path the policy actually guards; system:masters keeps the request
			// authorized so admission is what decides the outcome.
			restConfig, err := tc.GetHostedClusterRESTConfig(hc)
			Expect(err).NotTo(HaveOccurred())
			restConfig = securityAdmissionImpersonation(restConfig)
			hcClient, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
			Expect(err).NotTo(HaveOccurred())

			Expect(securityDeniedBindingDeletions(tc.Context, hcClient)).To(Succeed())
		})

		It("should allow status modifications via VAPs", func() {
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			network := &configv1.Network{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, network)).To(Succeed())
			originalMTU := network.Status.ClusterNetworkMTU
			DeferCleanup(func() {
				Eventually(func(g Gomega) {
					g.Expect(securitySetNetworkMTU(tc.Context, hcClient, originalMTU)).To(Succeed(),
						"cleanup: failed to restore original ClusterNetworkMTU")
				}, time.Minute, 5*time.Second).Should(Succeed())
			})
			Eventually(func(g Gomega) {
				g.Expect(securitySetNetworkMTU(tc.Context, hcClient, originalMTU+1)).To(Succeed(),
					"VAP should allow status modifications")
			}, time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should allow OperatorHub config changes with guest OLM placement", func() {
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hostedCluster)
			Expect(err).NotTo(HaveOccurred())
			if hostedCluster.Spec.OLMCatalogPlacement != hyperv1.GuestOLMCatalogPlacement {
				Skip("OperatorHub test requires guest OLM catalog placement")
			}
			operatorHub := &configv1.OperatorHub{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, operatorHub)).To(Succeed())
			originalDisableAll := operatorHub.Spec.DisableAllDefaultSources
			DeferCleanup(func() {
				Eventually(func(g Gomega) {
					g.Expect(securitySetOperatorHubSources(tc.Context, hcClient, originalDisableAll)).To(Succeed(),
						"cleanup: failed to restore OperatorHub configuration")
				}, time.Minute, 5*time.Second).Should(Succeed())
			})
			Eventually(func(g Gomega) {
				g.Expect(securitySetOperatorHubSources(tc.Context, hcClient, !originalDisableAll)).To(Succeed(),
					"VAP should allow OperatorHub configuration changes when OLM uses guest placement")
			}, time.Minute, 5*time.Second).Should(Succeed())
		})
	})
}

func EnsureNetworkPoliciesTest(getTestCtx internal.TestContextGetter) {
	When("[Feature:NetworkPolicies] checking network policies on an AWS hosted cluster", Label("hosted-cluster-network-policies"), func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		})

		It("should find management KAS access labels on expected components", func() {
			tc := getTestCtx()

			podList := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, podList,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{suppconfig.NeedManagementKASAccessLabel: "true"},
			)).To(Succeed())
			Expect(podList.Items).NotTo(BeEmpty(),
				"expected pods with %s label in namespace %s",
				suppconfig.NeedManagementKASAccessLabel, tc.ControlPlaneNamespace)
			allowed := securityManagementKASComponents()
			for _, pod := range podList.Items {
				component := securityPodComponent(pod)
				Expect(component).NotTo(BeEmpty(), "cannot identify privileged pod %s", pod.Name)
				Expect(allowed.Has(component)).To(BeTrue(), "component %s must not have management KAS access", component)
			}
		})

		It("should block egress traffic from non-privileged pods to the management KAS", func() {
			tc := getTestCtx()

			mgmtRestConfig, err := v2util.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "should be able to load management cluster REST config")
			clientset, err := kubernetes.NewForConfig(mgmtRestConfig)
			Expect(err).NotTo(HaveOccurred(), "should be able to create kubernetes clientset")

			endpoints := &corev1.Endpoints{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Name: "kubernetes", Namespace: "default"}, endpoints)).To(Succeed())
			var kasAddress string
			for _, subset := range endpoints.Subsets {
				if len(subset.Addresses) > 0 && len(subset.Ports) > 0 {
					kasAddress = "https://" + net.JoinHostPort(subset.Addresses[0].IP, fmt.Sprintf("%d", subset.Ports[0].Port))
					break
				}
			}
			Expect(kasAddress).NotTo(BeEmpty(), "should resolve management KAS endpoint address")

			Eventually(func(g Gomega) {
				cvoPods := &corev1.PodList{}
				g.Expect(tc.MgmtClient.List(tc.Context, cvoPods,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": "cluster-version-operator"},
				)).To(Succeed())
				g.Expect(cvoPods.Items).NotTo(BeEmpty(), "cluster-version-operator pod should exist")

				var runningPodName string
				for _, p := range cvoPods.Items {
					if p.Status.Phase == corev1.PodRunning {
						runningPodName = p.Name
						break
					}
				}
				g.Expect(runningPodName).NotTo(BeEmpty(), "a running cluster-version-operator pod should exist")

				_, err := v2util.RunCommandInPod(tc.Context, clientset, mgmtRestConfig,
					tc.ControlPlaneNamespace, runningPodName, "cluster-version-operator",
					"curl", "--connect-timeout", "2", "-Iks", kasAddress)
				g.Expect(err).To(HaveOccurred(),
					"cluster-version-operator should not be able to reach the management KAS at %s", kasAddress)
				g.Expect(securityCurlConnectionDenied(err)).To(BeTrue(), "expected curl connection failure or timeout, not exec/setup failure: %v", err)
			}, 1*time.Minute, 10*time.Second).Should(Succeed())

			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform &&
				hostedCluster.Spec.Platform.AWS != nil &&
				hostedCluster.Spec.Platform.AWS.EndpointAccess != hyperv1.Private {
				By("Verifying private-router cannot reach management KAS")
				routerPods := &corev1.PodList{}
				Expect(tc.MgmtClient.List(tc.Context, routerPods,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": "private-router"},
				)).To(Succeed())
				Expect(routerPods.Items).NotTo(BeEmpty(), "private-router pod must exist")
				if len(routerPods.Items) > 0 {
					Eventually(func(g Gomega) {
						currentRouterPods := &corev1.PodList{}
						g.Expect(tc.MgmtClient.List(tc.Context, currentRouterPods,
							crclient.InNamespace(tc.ControlPlaneNamespace),
							crclient.MatchingLabels{"app": "private-router"},
						)).To(Succeed())
						g.Expect(currentRouterPods.Items).NotTo(BeEmpty(), "private-router pod should exist")

						var runningPodName string
						for _, p := range currentRouterPods.Items {
							if p.Status.Phase == corev1.PodRunning {
								runningPodName = p.Name
								break
							}
						}
						g.Expect(runningPodName).NotTo(BeEmpty(), "a running private-router pod should exist")

						_, err := v2util.RunCommandInPod(tc.Context, clientset, mgmtRestConfig,
							tc.ControlPlaneNamespace, runningPodName, "router",
							"curl", "--connect-timeout", "2", "-Iks", kasAddress)
						g.Expect(err).To(HaveOccurred(),
							"private-router should not be able to reach the management KAS at %s", kasAddress)
						g.Expect(securityCurlConnectionDenied(err)).To(BeTrue(), "expected curl connection failure or timeout, not exec/setup failure: %v", err)
					}, 1*time.Minute, 10*time.Second).Should(Succeed())
				}
			}
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift] Hosted Cluster Security", Label("hosted-cluster-security"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterSecurityTests(func() *internal.TestContext { return testCtx })
})
