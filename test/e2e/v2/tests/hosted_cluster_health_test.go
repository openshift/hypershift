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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcc "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/support/conditions"
	hyperutil "github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// RegisterHostedClusterHealthTests registers all hosted cluster health test specs.
func RegisterHostedClusterHealthTests(getTestCtx internal.TestContextGetter) {
	ValidateHostedClusterConditionsTest(getTestCtx)
	EnsureCAPIFinalizersTest(getTestCtx)
	EnsureFeatureGateStatusTest(getTestCtx)
	EnsurePayloadArchSetCorrectlyTest(getTestCtx)
	ValidateConfigurationStatusTest(getTestCtx)
}

func ValidateHostedClusterConditionsTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster is operational", func() {
		It("should have all expected conditions with correct status", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()

			expectedConditions := conditions.ExpectedHCConditions(hostedCluster)
			delete(expectedConditions, hyperv1.KubeVirtNodesLiveMigratable)
			if e2eutil.IsLessThan(e2eutil.Version421) {
				delete(expectedConditions, hyperv1.DataPlaneConnectionAvailable)
			}
			if e2eutil.IsLessThan(e2eutil.Version422) {
				delete(expectedConditions, hyperv1.ControlPlaneConnectionAvailable)
				delete(expectedConditions, hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC)
			}
			if e2eutil.IsLessThan(e2eutil.Version423) {
				delete(expectedConditions, hyperv1.ConfigOperatorReconciliationSucceeded)
			}

			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				for condType, expectedStatus := range expectedConditions {
					condition := meta.FindStatusCondition(hc.Status.Conditions, string(condType))
					g.Expect(condition).NotTo(BeNil(), "condition %s should be present", condType)
					g.Expect(condition.Status).To(Equal(expectedStatus), "condition %s should have status %s", condType, expectedStatus)
				}
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func EnsureCAPIFinalizersTest(getTestCtx internal.TestContextGetter) {
	When("CAPI components are deployed", func() {
		It("should have component finalizers on all CAPI deployments", func() {
			tc := getTestCtx()
			Expect(hcc.CAPIComponents).NotTo(BeEmpty(),
				"expected CAPI components to be defined in HostedControlPlaneConfiguration")
			for _, name := range hcc.CAPIComponents {
				deployment := &appsv1.Deployment{}
				Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Name:      name,
					Namespace: tc.ControlPlaneNamespace,
				}, deployment)).To(Succeed(), "failed to get CAPI deployment %s", name)
				Expect(controllerutil.ContainsFinalizer(deployment, hcc.ControlPlaneComponentFinalizer)).To(BeTrue(),
					"CAPI deployment %s should have finalizer %s", name, hcc.ControlPlaneComponentFinalizer)
			}
		})
	})
}

func EnsureFeatureGateStatusTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster version is completed", func() {
		It("should have feature gate status matching cluster version", func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version419) {
				Skip("Feature gate status test requires version >= 4.19")
			}
			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

			var currentVersion string
			Eventually(func(g Gomega) {
				cv := &configv1.ClusterVersion{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "version"}, cv)).To(Succeed())
				g.Expect(cv.Status.History).NotTo(BeEmpty())
				g.Expect(cv.Status.History[0].State).To(Equal(configv1.CompletedUpdate))
				currentVersion = cv.Status.History[0].Version
			}, 30*time.Minute, 30*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				fg := &configv1.FeatureGate{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, fg)).To(Succeed())
				found := false
				for _, details := range fg.Status.FeatureGates {
					if details.Version == currentVersion {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "version %s not found in FeatureGate status", currentVersion)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func EnsurePayloadArchSetCorrectlyTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has a release image", func() {
		It("should set payload arch status correctly", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()

			imageMetadataProvider := &hyperutil.RegistryClientImageMetadataProvider{}
			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				g.Expect(hc.Status.PayloadArch).NotTo(BeEmpty(), "PayloadArch should be set")
				payloadArch, err := hyperutil.DetermineHostedClusterPayloadArch(tc.Context, tc.MgmtClient, hc, imageMetadataProvider)
				g.Expect(err).NotTo(HaveOccurred(), "failed to determine payload arch")
				g.Expect(payloadArch).To(Equal(hc.Status.PayloadArch))
			}, 30*time.Minute, time.Minute).Should(Succeed())
		})
	})
}

func ValidateConfigurationStatusTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster authentication is configured", func() {
		It("should propagate configuration status consistently", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()
			if e2eutil.IsLessThan(e2eutil.Version421) {
				Skip("Configuration status requires version >= 4.21")
			}
			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

			Eventually(func(g Gomega) {
				var hostedClusterAuth configv1.Authentication
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, &hostedClusterAuth)).To(Succeed())

				var hcp hyperv1.HostedControlPlane
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Name:      hostedCluster.Name,
					Namespace: tc.ControlPlaneNamespace,
				}, &hcp)).To(Succeed())
				g.Expect(hcp.Status.Configuration).NotTo(BeNil(), "HCP configuration status not set")

				var hc hyperv1.HostedCluster
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), &hc)).To(Succeed())
				g.Expect(hc.Status.Configuration).NotTo(BeNil(), "HC configuration status not set")

				g.Expect(hcp.Status.Configuration.Authentication).To(Equal(hostedClusterAuth.Status),
					"HCP authentication status should match hosted cluster Authentication resource")
				g.Expect(hc.Status.Configuration.Authentication).To(Equal(hostedClusterAuth.Status),
					"HC authentication status should match hosted cluster Authentication resource")
				g.Expect(hcp.Status.Configuration.Authentication).To(Equal(hc.Status.Configuration.Authentication),
					"HCP and HC authentication status should be consistent")
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Health] Hosted Cluster Health", Label("hosted-cluster-health"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterHealthTests(func() *internal.TestContext { return testCtx })
})
