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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/operatorconfig"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// logLevelPath is the operatorConfiguration setting this test configures. It is a real,
// day-2 mutable setting, which is what makes the end-to-end assertion meaningful: the
// path has to travel from the HostedCluster spec, through the HostedControlPlane, into
// what the control plane operator reports it honors.
const logLevelPath = "kubeAPIServer.logLevel"

func RegisterOperatorConfigurationHonoredTests(getTestCtx internal.TestContextGetter) {
	OperatorConfigurationHonoredTest(getTestCtx)
}

// hostedControlPlane fetches the HostedControlPlane backing the hosted cluster.
func hostedControlPlane(g Gomega, tc *internal.TestContext) *hyperv1.HostedControlPlane {
	GinkgoHelper()
	hcpList := &hyperv1.HostedControlPlaneList{}
	g.Expect(tc.MgmtClient.List(tc.Context, hcpList, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
	g.Expect(hcpList.Items).To(HaveLen(1), "exactly one HostedControlPlane should exist in %s", tc.ControlPlaneNamespace)
	return &hcpList.Items[0]
}

// operatorConfigurationHonoredCondition returns the HostedCluster's
// OperatorConfigurationHonored condition, which the HyperShift operator always sets.
func operatorConfigurationHonoredCondition(g Gomega, tc *internal.TestContext, hc *hyperv1.HostedCluster) *metav1.Condition {
	GinkgoHelper()
	current := &hyperv1.HostedCluster{}
	g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)).To(Succeed())
	condition := meta.FindStatusCondition(current.Status.Conditions, string(hyperv1.OperatorConfigurationHonored))
	g.Expect(condition).NotTo(BeNil(), "the HyperShift operator should always set the %s condition", hyperv1.OperatorConfigurationHonored)
	return condition
}

// logLevelInCRDSchema reports whether the installed HostedCluster CRD exposes
// spec.operatorConfiguration.kubeAPIServer.logLevel. The setting is gated on
// HCPUserFacingOperatorLogs, so on a Default management cluster it is absent from the
// schema and the API server prunes it from any write, which would leave this test
// waiting for a propagation that can never happen.
func logLevelInCRDSchema(g Gomega, tc *internal.TestContext) bool {
	GinkgoHelper()
	crd := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Name: "hostedclusters.hypershift.openshift.io"}, crd)).To(Succeed())

	for _, version := range crd.Spec.Versions {
		if version.Name != hyperv1.GroupVersion.Version || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			continue
		}
		schema := version.Schema.OpenAPIV3Schema
		for _, field := range []string{"spec", "operatorConfiguration", "kubeAPIServer", "logLevel"} {
			next, ok := schema.Properties[field]
			if !ok {
				return false
			}
			schema = &next
		}
		return true
	}
	return false
}

// restoreLogLevelPatch builds the merge patch that undoes setting logLevel on original.
// KubeAPIServerOperatorSpec carries MinProperties=1, so clearing logLevel alone would
// leave an empty kubeAPIServer object and be rejected at admission. The whole object, or
// the whole operatorConfiguration, has to go when the test introduced it.
func restoreLogLevelPatch(original *hyperv1.HostedCluster) []byte {
	switch {
	case original.Spec.OperatorConfiguration == nil:
		return []byte(`{"spec":{"operatorConfiguration":null}}`)
	case original.Spec.OperatorConfiguration.KubeAPIServer == (hyperv1.KubeAPIServerOperatorSpec{}):
		return []byte(`{"spec":{"operatorConfiguration":{"kubeAPIServer":null}}}`)
	default:
		return []byte(fmt.Sprintf(`{"spec":{"operatorConfiguration":{"kubeAPIServer":{"logLevel":%q}}}}`,
			original.Spec.OperatorConfiguration.KubeAPIServer.LogLevel))
	}
}

func OperatorConfigurationHonoredTest(getTestCtx internal.TestContextGetter) {
	When("the control plane operator is the same version as the HyperShift operator", func() {
		var tc *internal.TestContext
		var hc *hyperv1.HostedCluster

		BeforeEach(func() {
			tc = getTestCtx()

			var err error
			hc, err = tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should report every configured operatorConfiguration setting as honored", Label("operator-configuration-honored", internal.InformingLabel), func() {
			By("confirming the control plane operator reports exactly the settings the spec configures")
			// This is the contract the condition rests on. The control plane operator
			// derives its report from its own build's view of the spec, so on a cluster
			// with no version skew it has to match what the HyperShift operator derives
			// from the same spec. Any mismatch here is a real skew signal, not a flake.
			Eventually(func(g Gomega) {
				hcp := hostedControlPlane(g, tc)
				configured, err := operatorconfig.FieldPaths(hcp.Spec.OperatorConfiguration)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(hcp.Status.OperatorConfiguration.ObservedGeneration).To(Equal(hcp.Generation),
					"the control plane operator should have observed the current HostedControlPlane spec")
				g.Expect(operatorconfig.Unhonored(configured, hcp.Status.OperatorConfiguration.HonoredFields)).To(BeEmpty(),
					"a control plane operator at the same version should honor every configured setting")
			}).WithContext(tc.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("confirming the HostedCluster reports the settings as honored")
			Eventually(func(g Gomega) {
				condition := operatorConfigurationHonoredCondition(g, tc, hc)
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue),
					"reason: %s, message: %s", condition.Reason, condition.Message)
				g.Expect(condition.Reason).To(Equal(hyperv1.AsExpectedReason))
			}).WithContext(tc.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("confirming the condition stays honored rather than flapping")
			Consistently(func(g Gomega) {
				condition := operatorConfigurationHonoredCondition(g, tc, hc)
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue),
					"reason: %s, message: %s", condition.Reason, condition.Message)
			}).WithContext(tc.Context).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})

		It("should track a newly configured setting through to the control plane operator", Label("operator-configuration-honored", internal.InformingLabel), func() {
			if !logLevelInCRDSchema(Default, tc) {
				Skip("spec.operatorConfiguration.kubeAPIServer.logLevel is gated on HCPUserFacingOperatorLogs " +
					"and is absent from the installed HostedCluster CRD. Set HYPERSHIFT_FEATURESET=TechPreviewNoUpgrade " +
					"on the HyperShift operator to enable it.")
			}
			if hc.Spec.OperatorConfiguration != nil && hc.Spec.OperatorConfiguration.KubeAPIServer.LogLevel == hyperv1.Debug {
				Skip("HostedCluster already sets spec.operatorConfiguration.kubeAPIServer.logLevel to Debug")
			}

			// The setting is day-2 mutable, so restore the captured configuration rather
			// than leaving the hosted cluster configured for whichever test runs next.
			restorePatch := restoreLogLevelPatch(hc)
			DeferCleanup(func() {
				Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, restorePatch))).To(Succeed())
			})

			By("confirming the setting is not yet reported as honored")
			Eventually(func(g Gomega) {
				hcp := hostedControlPlane(g, tc)
				g.Expect(hcp.Status.OperatorConfiguration.HonoredFields).NotTo(ContainElement(logLevelPath))
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("setting logLevel on the HostedCluster")
			patch := fmt.Appendf(nil, `{"spec":{"operatorConfiguration":{"kubeAPIServer":{"logLevel":%q}}}}`, hyperv1.Debug)
			Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

			By("waiting for the control plane operator to report the setting as honored")
			Eventually(func(g Gomega) {
				hcp := hostedControlPlane(g, tc)
				g.Expect(hcp.Status.OperatorConfiguration.HonoredFields).To(ContainElement(logLevelPath),
					"a control plane operator that supports the setting should report it as honored")
			}).WithContext(tc.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("confirming the HostedCluster still reports the configuration as honored")
			Eventually(func(g Gomega) {
				condition := operatorConfigurationHonoredCondition(g, tc, hc)
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue),
					"reason: %s, message: %s", condition.Reason, condition.Message)
				g.Expect(condition.Reason).To(Equal(hyperv1.AsExpectedReason))
			}).WithContext(tc.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:OperatorConfiguration] Hosted Cluster Operator Configuration Honored",
	Label("lifecycle", "hosted-cluster-operator-configuration-honored", internal.InformingLabel), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		})

		RegisterOperatorConfigurationHonoredTests(func() *internal.TestContext { return testCtx })
	})
