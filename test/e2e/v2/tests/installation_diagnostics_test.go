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
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterInstallationDiagnosticsTests registers all installation diagnostics test specs.
func RegisterInstallationDiagnosticsTests(getTestCtx internal.TestContextGetter) {
	ValidateInstallationStageCompleteTest(getTestCtx)
	ValidateManagementClusterResourcePressureTest(getTestCtx)
}

// ValidateInstallationStageCompleteTest verifies that a fully installed hosted cluster
// has its installation stage set to InstallationComplete on both the HostedCluster and
// the HostedControlPlane, and that the lastTransitionTime is populated.
func ValidateInstallationStageCompleteTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster is fully installed", func() {
		It("should have installation stage set to InstallationComplete", func() {
			tc := getTestCtx()
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				g.Expect(hc.Status.Installation).NotTo(BeNil(),
					"installation status should be set on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(hc.Status.Installation.Stage).To(Equal(hyperv1.InstallationStageInstallationComplete),
					"installation stage should be InstallationComplete on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(hc.Status.Installation.LastTransitionTime.IsZero()).To(BeFalse(),
					"installation lastTransitionTime should be set on HostedCluster %s/%s", hc.Namespace, hc.Name)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				hcp := &hyperv1.HostedControlPlane{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Name:      hostedCluster.Name,
					Namespace: tc.ControlPlaneNamespace,
				}, hcp)).To(Succeed())
				g.Expect(hcp.Status.Installation).NotTo(BeNil(),
					"installation status should be set on HostedControlPlane %s/%s", hcp.Namespace, hcp.Name)
				g.Expect(hcp.Status.Installation.Stage).To(Equal(hyperv1.InstallationStageInstallationComplete),
					"installation stage should be InstallationComplete on HostedControlPlane %s/%s", hcp.Namespace, hcp.Name)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// ValidateManagementClusterResourcePressureTest verifies that a healthy hosted cluster
// has the ManagementClusterResourcePressure condition set to False with reason NoPressure.
func ValidateManagementClusterResourcePressureTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has no scheduling pressure", func() {
		It("should have ManagementClusterResourcePressure set to False", func() {
			tc := getTestCtx()
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
				g.Expect(cond).NotTo(BeNil(),
					"ManagementClusterResourcePressure condition should be present on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
					"ManagementClusterResourcePressure should be False on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(cond.Reason).To(Equal(hyperv1.ReasonNoPressure),
					"ManagementClusterResourcePressure reason should be NoPressure on HostedCluster %s/%s", hc.Namespace, hc.Name)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:InstallationDiagnostics] Installation Diagnostics",
	Label("installation-diagnostics"), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		})

		RegisterInstallationDiagnosticsTests(func() *internal.TestContext { return testCtx })
	})
