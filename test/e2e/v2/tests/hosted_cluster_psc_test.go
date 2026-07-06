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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GCPPrivateServiceConnectTest registers tests that validate PSC resource correctness on GCP.
func GCPPrivateServiceConnectTest(getTestCtx internal.TestContextGetter) {
	Context("GCP Private Service Connect", Label("GCP", "PSC"), func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.GCPPlatform {
				Skip("GCP Private Service Connect test is only for GCP platform")
			}
		})

		// GCP enforces that the NAT subnet and the forwarding rule must belong to the same VPC
		// when creating a Service Attachment. A True GCPServiceAttachmentAvailable condition
		// is therefore proof that the controller selected a subnet from the correct VPC.
		It("should have GCPServiceAttachmentAvailable condition set to True", func() {
			testCtx := getTestCtx()

			// Find the GCPPrivateServiceConnect CR in the control plane namespace.
			// There is exactly one GCPPrivateServiceConnect per hosted cluster.
			pscList := &hyperv1.GCPPrivateServiceConnectList{}
			Expect(testCtx.MgmtClient.List(testCtx.Context, pscList,
				crclient.InNamespace(testCtx.ControlPlaneNamespace),
			)).To(Succeed())
			Expect(pscList.Items).NotTo(BeEmpty(),
				"expected at least one GCPPrivateServiceConnect in namespace %s", testCtx.ControlPlaneNamespace)

			psc := pscList.Items[0]

			// Assert both spec fields are populated — the controller successfully resolved
			// the forwarding rule and the VPC-scoped NAT subnet.
			Expect(string(psc.Spec.ForwardingRuleName)).NotTo(BeEmpty(),
				"GCPPrivateServiceConnect %s should have ForwardingRuleName set", psc.Name)
			Expect(string(psc.Spec.NATSubnet)).NotTo(BeEmpty(),
				"GCPPrivateServiceConnect %s should have NATSubnet set", psc.Name)

			// Assert GCP accepted the Service Attachment. GCP rejects a Service Attachment
			// whose NAT subnet is not in the same VPC as the forwarding rule, so a True
			// condition here implicitly validates VPC correctness without requiring GCP
			// credentials in the test binary.
			found := false
			for _, cond := range psc.Status.Conditions {
				if cond.Type == string(hyperv1.GCPServiceAttachmentAvailable) {
					found = true
					Expect(cond.Status).To(Equal(metav1.ConditionTrue),
						"GCPServiceAttachmentAvailable condition should be True on GCPPrivateServiceConnect %s: %s",
						psc.Name, cond.Message)
					break
				}
			}
			Expect(found).To(BeTrue(),
				"expected GCPServiceAttachmentAvailable condition on GCPPrivateServiceConnect %s", psc.Name)
		})
	})
}

// RegisterGCPPSCTests registers all GCP Private Service Connect tests.
func RegisterGCPPSCTests(getTestCtx internal.TestContextGetter) {
	GCPPrivateServiceConnectTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GCPPrivateServiceConnect] GCP Private Service Connect", Label("gcp-psc"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		testCtx.ValidateHostedCluster()
	})

	RegisterGCPPSCTests(func() *internal.TestContext { return testCtx })
})
