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

	configv1 "github.com/openshift/api/config/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hccokasvap "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kas"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
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
	EnsureGuestWebhooksValidatedTest(getTestCtx)
	EnsureAdmissionPoliciesTest(getTestCtx)
	EnsureNetworkPoliciesTest(getTestCtx)
}

func EnsureGuestWebhooksValidatedTest(getTestCtx internal.TestContextGetter) {
	When("a webhook targeting a control plane service is created in the hosted cluster", func() {
		It("should be automatically deleted", func() {
			tc := getTestCtx()
			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

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
			}, time.Minute, 5*time.Second).Should(Succeed())
		})
	})
}

func EnsureAdmissionPoliciesTest(getTestCtx internal.TestContextGetter) {
	When("checking admission policies on a public hosted cluster", func() {
		It("should find all required ValidatingAdmissionPolicies", func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version418) {
				Skip("Admission policies require version >= 4.18")
			}
			hostedCluster := tc.GetHostedCluster()

			if !netutil.IsPublicHC(hostedCluster) {
				Skip("admission policies test requires a public hosted cluster")
			}

			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

			Eventually(func(g Gomega) {
				vapList := &admissionregistrationv1.ValidatingAdmissionPolicyList{}
				g.Expect(hcClient.List(tc.Context, vapList)).To(Succeed())
				g.Expect(vapList.Items).NotTo(BeEmpty(), "expected ValidatingAdmissionPolicies to be present")

				requiredVAPs := []string{
					hccokasvap.AdmissionPolicyNameConfig,
					hccokasvap.AdmissionPolicyNameMirror,
					hccokasvap.AdmissionPolicyNameICSP,
					hccokasvap.AdmissionPolicyNameInfra,
					hccokasvap.AdmissionPolicyNameNTOMirroredConfigs,
				}
				vapNames := make([]string, 0, len(vapList.Items))
				for _, vap := range vapList.Items {
					vapNames = append(vapNames, vap.Name)
				}
				for _, required := range requiredVAPs {
					g.Expect(vapNames).To(ContainElement(required),
						"required ValidatingAdmissionPolicy %s not found", required)
				}
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Verifying VAPs deny unauthorized config changes")
			Eventually(func(g Gomega) {
				apiServer := &configv1.APIServer{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, apiServer)).To(Succeed())
				apiServerCopy := apiServer.DeepCopy()
				apiServerCopy.Spec.Audit.Profile = configv1.AllRequestBodiesAuditProfileType
				err := hcClient.Update(tc.Context, apiServerCopy)
				g.Expect(err).To(HaveOccurred(), "VAP should block audit profile modification")
				g.Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy"),
					"rejection should be from a ValidatingAdmissionPolicy, got: %v", err)
			}, time.Minute, 5*time.Second).Should(Succeed())

			By("Verifying VAPs allow status modifications")
			network := &configv1.Network{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, network)).To(Succeed())
			originalMTU := network.Status.ClusterNetworkMTU
			DeferCleanup(func() {
				Eventually(func(g Gomega) {
					net := &configv1.Network{}
					g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, net)).To(Succeed(),
						"cleanup: failed to get Network resource for MTU restoration")
					net.Status.ClusterNetworkMTU = originalMTU
					g.Expect(hcClient.Update(tc.Context, net)).To(Succeed(),
						"cleanup: failed to restore original ClusterNetworkMTU")
				}, time.Minute, 5*time.Second).Should(Succeed())
			})
			Eventually(func(g Gomega) {
				networkCopy := &configv1.Network{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, networkCopy)).To(Succeed())
				networkCopy.Status.ClusterNetworkMTU = 9180
				g.Expect(hcClient.Update(tc.Context, networkCopy)).To(Succeed(),
					"VAP should allow status modifications")
			}, time.Minute, 5*time.Second).Should(Succeed())

			if hostedCluster.Spec.OLMCatalogPlacement == hyperv1.GuestOLMCatalogPlacement {
				By("Verifying VAPs allow OperatorHub configuration changes")
				operatorHub := &configv1.OperatorHub{}
				Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, operatorHub)).To(Succeed())
				originalDisableAll := operatorHub.Spec.DisableAllDefaultSources
				DeferCleanup(func() {
					Eventually(func(g Gomega) {
						oh := &configv1.OperatorHub{}
						g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, oh)).To(Succeed(),
							"cleanup: failed to get OperatorHub resource")
						oh.Spec.DisableAllDefaultSources = originalDisableAll
						g.Expect(hcClient.Update(tc.Context, oh)).To(Succeed(),
							"cleanup: failed to restore OperatorHub configuration")
					}, time.Minute, 5*time.Second).Should(Succeed())
				})
				operatorHubCopy := operatorHub.DeepCopy()
				operatorHubCopy.Spec.DisableAllDefaultSources = true
				Expect(hcClient.Update(tc.Context, operatorHubCopy)).To(Succeed(),
					"VAP should allow OperatorHub configuration changes when OLM uses guest placement")
			}
		})
	})
}

func EnsureNetworkPoliciesTest(getTestCtx internal.TestContextGetter) {
	When("checking network policies on an AWS hosted cluster", func() {
		It("should find management KAS access labels on expected components", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()
			if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("network policies test is only for AWS platform")
			}

			podList := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, podList,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{suppconfig.NeedManagementKASAccessLabel: "true"},
			)).To(Succeed())
			Expect(podList.Items).NotTo(BeEmpty(),
				"expected pods with %s label in namespace %s",
				suppconfig.NeedManagementKASAccessLabel, tc.ControlPlaneNamespace)
		})

		It("should block egress traffic from non-privileged pods to the management KAS", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()
			if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("network policies test is only for AWS platform")
			}

			mgmtRestConfig, err := e2eutil.GetConfig()
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

			cvoPods := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, cvoPods,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{"app": "cluster-version-operator"},
			)).To(Succeed())
			Expect(cvoPods.Items).NotTo(BeEmpty(), "cluster-version-operator pod should exist")

			_, err = v2util.RunCommandInPod(tc.Context, clientset, mgmtRestConfig,
				tc.ControlPlaneNamespace, cvoPods.Items[0].Name, "cluster-version-operator",
				"curl", "--connect-timeout", "2", "-Iks", kasAddress)
			Expect(err).To(HaveOccurred(),
				"cluster-version-operator should not be able to reach the management KAS at %s", kasAddress)
		})
	})
}

var _ = Describe("Hosted Cluster Security", Label("hosted-cluster-security"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterSecurityTests(func() *internal.TestContext { return testCtx })
})
