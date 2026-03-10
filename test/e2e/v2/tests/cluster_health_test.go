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
	"fmt"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hccokasvap "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kas"
	hcc "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	hyperutil "github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	k8sadmissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// isKubeVirtPod returns true if the pod is a KubeVirt-managed pod that should be
// excluded from control plane validation checks.
func isKubeVirtPod(pod *corev1.Pod) bool {
	return pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug"
}

// FeatureGateStatusTest validates that the guest cluster FeatureGate status contains
// the current version from ClusterVersion.
func FeatureGateStatusTest(getTestCtx internal.TestContextGetter) {
	Context("Feature gate status", func() {
		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version419)
		})

		It("When checking feature gates it should contain the current cluster version", func() {
			testCtx := getTestCtx()
			guestClient := testCtx.GetGuestClient()

			// Wait for ClusterVersion to have a completed history entry
			var currentVersion string
			Eventually(func(g Gomega) {
				cv := &configv1.ClusterVersion{}
				err := guestClient.Get(testCtx, crclient.ObjectKey{Name: "version"}, cv)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cv.Status.History).NotTo(BeEmpty(), "ClusterVersion history is empty")
				g.Expect(cv.Status.History[0].State).To(Equal(configv1.CompletedUpdate),
					"most recent ClusterVersion history entry is %s, not Completed", cv.Status.History[0].State)
				currentVersion = cv.Status.History[0].Version
			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			// Verify FeatureGate contains the current version
			Eventually(func(g Gomega) {
				fg := &configv1.FeatureGate{}
				err := guestClient.Get(testCtx, crclient.ObjectKey{Name: "cluster"}, fg)
				g.Expect(err).NotTo(HaveOccurred())
				found := false
				for _, details := range fg.Status.FeatureGates {
					if details.Version == currentVersion {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(),
					"current version %s from ClusterVersion not found in FeatureGate status", currentVersion)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// CAPIFinalizersTest validates that CAPI component deployments have the expected finalizer.
func CAPIFinalizersTest(getTestCtx internal.TestContextGetter) {
	Context("CAPI finalizers", func() {
		It("When checking CAPI deployments it should have the component finalizer", func() {
			testCtx := getTestCtx()

			for _, name := range hcc.CAPIComponents {
				deployment := &appsv1.Deployment{}
				err := testCtx.MgmtClient.Get(testCtx, crclient.ObjectKey{
					Namespace: testCtx.ControlPlaneNamespace,
					Name:      name,
				}, deployment)
				Expect(err).NotTo(HaveOccurred(), "failed to get CAPI deployment %s", name)
				Expect(controllerutil.ContainsFinalizer(deployment, hcc.ControlPlaneComponentFinalizer)).To(BeTrue(),
					"CAPI deployment %s is expected to have finalizer: %s", name, hcc.ControlPlaneComponentFinalizer)
			}
		})
	})
}

// GuestWebhooksValidatedTest validates that invalid guest webhooks are automatically deleted.
func GuestWebhooksValidatedTest(getTestCtx internal.TestContextGetter) {
	Context("Guest webhooks validated", func() {
		It("When creating an invalid webhook it should be automatically deleted", func() {
			testCtx := getTestCtx()
			guestClient := testCtx.GetGuestClient()

			sideEffectsNone := k8sadmissionv1.SideEffectClassNone
			guestWebhookConf := &k8sadmissionv1.ValidatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-webhook",
					Annotations: map[string]string{
						"service.beta.openshift.io/inject-cabundle": "true",
					},
				},
				Webhooks: []k8sadmissionv1.ValidatingWebhook{{
					AdmissionReviewVersions: []string{"v1"},
					Name:                    "etcd-client.example.com",
					ClientConfig: k8sadmissionv1.WebhookClientConfig{
						URL: ptr.To("https://etcd-client:2379"),
					},
					Rules: []k8sadmissionv1.RuleWithOperations{{
						Operations: []k8sadmissionv1.OperationType{k8sadmissionv1.Create},
						Rule: k8sadmissionv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					}},
					SideEffects: &sideEffectsNone,
				}},
			}

			err := guestClient.Create(testCtx, guestWebhookConf)
			Expect(err).NotTo(HaveOccurred(), "failed to create test webhook")

			Eventually(func() bool {
				webhook := &k8sadmissionv1.ValidatingWebhookConfiguration{}
				err := guestClient.Get(testCtx, crclient.ObjectKeyFromObject(guestWebhookConf), webhook)
				return apierrors.IsNotFound(err)
			}, 1*time.Minute, 5*time.Second).Should(BeTrue(),
				"violating webhook %s was not deleted", guestWebhookConf.Name)
		})
	})
}

// PayloadArchTest validates that the HostedCluster status reports the correct payload architecture.
func PayloadArchTest(getTestCtx internal.TestContextGetter) {
	Context("Payload architecture", func() {
		It("When checking payload arch it should match the determined architecture", func() {
			testCtx := getTestCtx()
			hostedCluster, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				err := testCtx.MgmtClient.Get(testCtx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				g.Expect(err).NotTo(HaveOccurred())

				imageMetadataProvider := &hyperutil.RegistryClientImageMetadataProvider{}
				payloadArch, err := hyperutil.DetermineHostedClusterPayloadArch(
					testCtx, testCtx.MgmtClient, hc, imageMetadataProvider)
				g.Expect(err).NotTo(HaveOccurred(), "failed to determine payload arch")
				g.Expect(hc.Status.PayloadArch).To(Equal(payloadArch),
					"expected payload arch %s, got %s", payloadArch, hc.Status.PayloadArch)
			}, 30*time.Minute, 30*time.Second).Should(Succeed())
		})
	})
}

// AdmissionPoliciesTest validates validating admission policies in the guest cluster.
func AdmissionPoliciesTest(getTestCtx internal.TestContextGetter) {
	Context("Admission policies", func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hostedCluster, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			if !hyperutil.IsPublicHC(hostedCluster) {
				Skip("Admission policies are only validated for public clusters")
			}
		})

		It("When checking admission policies it should have all required ValidatingAdmissionPolicies", func() {
			testCtx := getTestCtx()
			hostedCluster, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			e2eutil.GinkgoCPOAtLeast(e2eutil.Version418, hostedCluster)

			guestClient := testCtx.GetGuestClient()
			var validatingAdmissionPolicies k8sadmissionv1.ValidatingAdmissionPolicyList
			err = guestClient.List(testCtx, &validatingAdmissionPolicies)
			Expect(err).NotTo(HaveOccurred(), "failed to list ValidatingAdmissionPolicies")
			Expect(validatingAdmissionPolicies.Items).NotTo(BeEmpty(), "no ValidatingAdmissionPolicies found")

			requiredVAPs := []string{
				hccokasvap.AdmissionPolicyNameConfig,
				hccokasvap.AdmissionPolicyNameMirror,
				hccokasvap.AdmissionPolicyNameICSP,
				hccokasvap.AdmissionPolicyNameInfra,
				hccokasvap.AdmissionPolicyNameNTOMirroredConfigs,
			}
			var presentVAPs []string
			for _, vap := range validatingAdmissionPolicies.Items {
				presentVAPs = append(presentVAPs, vap.Name)
			}
			for _, required := range requiredVAPs {
				Expect(presentVAPs).To(ContainElement(required),
					"ValidatingAdmissionPolicy %s not found", required)
			}
		})

		It("When checking admission policies it should deny unauthorized spec modifications", func() {
			testCtx := getTestCtx()
			hostedCluster, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			e2eutil.GinkgoCPOAtLeast(e2eutil.Version418, hostedCluster)

			guestClient := testCtx.GetGuestClient()
			apiServer := &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			}
			err = guestClient.Get(testCtx, crclient.ObjectKeyFromObject(apiServer), apiServer)
			Expect(err).NotTo(HaveOccurred(), "failed to get APIServer configuration")

			apiServerCopy := apiServer.DeepCopy()
			apiServerCopy.Spec.Audit.Profile = configv1.AllRequestBodiesAuditProfileType
			err = guestClient.Update(testCtx, apiServerCopy)
			Expect(err).To(HaveOccurred(), "APIServer spec update should have been denied")
		})

		It("When checking admission policies it should allow status modifications", func() {
			testCtx := getTestCtx()
			guestClient := testCtx.GetGuestClient()

			network := &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			}
			err := guestClient.Get(testCtx, crclient.ObjectKeyFromObject(network), network)
			Expect(err).NotTo(HaveOccurred(), "failed to get Network configuration")

			networkCopy := network.DeepCopy()
			networkCopy.Status.ClusterNetworkMTU = 9180
			err = guestClient.Update(testCtx, networkCopy)
			Expect(err).NotTo(HaveOccurred(), "Network status update should be allowed")
		})

		It("When checking admission policies it should allow OperatorHub changes with guest OLM placement", func() {
			testCtx := getTestCtx()
			hostedCluster, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if hostedCluster.Spec.OLMCatalogPlacement != hyperv1.GuestOLMCatalogPlacement {
				Skip("OperatorHub test only applicable with GuestOLMCatalogPlacement")
			}

			guestClient := testCtx.GetGuestClient()
			operatorHub := &configv1.OperatorHub{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			}
			err = guestClient.Get(testCtx, crclient.ObjectKeyFromObject(operatorHub), operatorHub)
			Expect(err).NotTo(HaveOccurred(), "failed to get OperatorHub")

			operatorHubCopy := operatorHub.DeepCopy()
			operatorHubCopy.Spec.DisableAllDefaultSources = true
			err = guestClient.Update(testCtx, operatorHubCopy)
			Expect(err).NotTo(HaveOccurred(), "OperatorHub update should be allowed")
		})
	})
}

// AllRoutesUseHCPRouterTest validates that all routes in the HCP namespace use the per-HCP router.
func AllRoutesUseHCPRouterTest(getTestCtx internal.TestContextGetter) {
	Context("Routes use HCP router", func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hostedCluster, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			for _, svc := range hostedCluster.Spec.Services {
				if svc.Service == hyperv1.APIServer && svc.Type != hyperv1.Route {
					Skip("APIServer is not exposed through a route")
				}
			}
		})

		It("When the API Server is exposed through a route, all routes should use the per-HCP router labels", func() {
			testCtx := getTestCtx()

			var routes routev1.RouteList
			err := testCtx.MgmtClient.List(testCtx, &routes, crclient.InNamespace(testCtx.ControlPlaneNamespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list routes")

			for _, route := range routes.Items {
				original := route.DeepCopy()
				hyperutil.AddHCPRouteLabel(&route)
				diff := cmp.Diff(route.GetLabels(), original.GetLabels())
				Expect(diff).To(BeEmpty(),
					"route %s is missing the label to use the per-HCP router: %s", route.Name, diff)
			}
		})
	})
}

// PSANotPrivilegedTest validates that Pod Security Admission enforcement rejects privileged pods.
func PSANotPrivilegedTest(getTestCtx internal.TestContextGetter) {
	Context("PSA not privileged", func() {
		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version421)
		})

		It("When PSA is enabled it should reject pods with HostPID", func() {
			testCtx := getTestCtx()
			guestClient := testCtx.GetGuestClient()

			// Check if OpenShiftPodSecurityAdmission feature gate is enabled
			featureGate := &configv1.FeatureGate{}
			err := guestClient.Get(testCtx, crclient.ObjectKey{Name: "cluster"}, featureGate)
			if err != nil {
				Skip(fmt.Sprintf("failed to get FeatureGate resource: %v", err))
			}

			var psaEnabled bool
			for _, details := range featureGate.Status.FeatureGates {
				for _, enabled := range details.Enabled {
					if enabled.Name == "OpenShiftPodSecurityAdmission" {
						psaEnabled = true
						break
					}
				}
				if psaEnabled {
					break
				}
			}
			if !psaEnabled {
				Skip("OpenShiftPodSecurityAdmission feature gate is not enabled")
			}

			testNamespaceName := "e2e-psa-check"
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceName},
			}
			err = guestClient.Create(testCtx, namespace)
			Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: testNamespaceName,
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						"e2e.openshift.io/unschedulable": "should-not-run",
					},
					Containers: []corev1.Container{
						{Name: "first", Image: "something-innocuous"},
					},
					HostPID: true,
				},
			}
			err = guestClient.Create(testCtx, pod)
			Expect(err).To(HaveOccurred(), "pod should have been rejected")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "expected Forbidden error, got %v", err)
		})
	})
}

// RegisterClusterHealthTests registers all cluster health validation tests.
func RegisterClusterHealthTests(getTestCtx internal.TestContextGetter) {
	FeatureGateStatusTest(getTestCtx)
	CAPIFinalizersTest(getTestCtx)
	GuestWebhooksValidatedTest(getTestCtx)
	PayloadArchTest(getTestCtx)
	AdmissionPoliciesTest(getTestCtx)
	AllRoutesUseHCPRouterTest(getTestCtx)
	PSANotPrivilegedTest(getTestCtx)
}

var _ = Describe("Cluster Health", Label("cluster-health"), func() {
	var (
		testCtx *internal.TestContext
	)

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		if testCtx.ControlPlaneNamespace == "" {
			AbortSuite("ControlPlaneNamespace is required but not set. Please set the following environment variables:\n" +
				"  E2E_HOSTED_CLUSTER_NAME - Name of the HostedCluster to test\n" +
				"  E2E_HOSTED_CLUSTER_NAMESPACE - Namespace of the HostedCluster to test")
		}
	})

	RegisterClusterHealthTests(func() *internal.TestContext { return testCtx })
})
