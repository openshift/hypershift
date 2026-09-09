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
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
)

// GCPWorkloadIdentityTest registers tests that validate GCP workload identity webhook mutation.
func GCPWorkloadIdentityTest(getTestCtx internal.TestContextGetter) {
	Context("GCP Workload Identity", Label("GCP", "gcp-wif"), func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			testCtx.SkipIfNotPlatform(hyperv1.GCPPlatform)
		})

		It("should mutate pods with workload identity federated credentials", func() {
			testCtx := getTestCtx()
			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			e2eutil.WaitForGuestKubeConfig(GinkgoTB(), testCtx.Context, testCtx.MgmtClient, hc)
			hostedClusterClient, err := testCtx.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			e2eutil.ValidateGCPWorkloadIdentityWebhookMutation(GinkgoTB(), testCtx.Context, hostedClusterClient)
		})
	})
}

// RegisterGCPWorkloadIdentityTests registers all GCP workload identity tests.
func RegisterGCPWorkloadIdentityTests(getTestCtx internal.TestContextGetter) {
	GCPWorkloadIdentityTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GCPWorkloadIdentity] GCP Workload Identity", Label("gcp-wif"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterGCPWorkloadIdentityTests(func() *internal.TestContext { return testCtx })
})
