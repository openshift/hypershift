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

	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

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

		It("should select a NAT subnet from the same VPC as the forwarding rule", func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			Expect(hc.Spec.Platform.GCP).NotTo(BeNil(),
				"GCP platform spec must be set for GCP HostedCluster %s/%s", hc.Namespace, hc.Name)

			project := hc.Spec.Platform.GCP.Project
			region := hc.Spec.Platform.GCP.Region

			// Find the GCPPrivateServiceConnect CR in the control plane namespace.
			pscList := &hyperv1.GCPPrivateServiceConnectList{}
			Expect(testCtx.MgmtClient.List(testCtx.Context, pscList,
				crclient.InNamespace(testCtx.ControlPlaneNamespace),
			)).To(Succeed())
			Expect(pscList.Items).NotTo(BeEmpty(),
				"expected at least one GCPPrivateServiceConnect in namespace %s", testCtx.ControlPlaneNamespace)

			psc := pscList.Items[0]
			Expect(string(psc.Spec.ForwardingRuleName)).NotTo(BeEmpty(),
				"GCPPrivateServiceConnect %s should have ForwardingRuleName set", psc.Name)
			Expect(string(psc.Spec.NATSubnet)).NotTo(BeEmpty(),
				"GCPPrivateServiceConnect %s should have NATSubnet set", psc.Name)

			// Build a GCP compute client using Application Default Credentials.
			httpClient, err := google.DefaultClient(testCtx.Context, compute.ComputeScope)
			Expect(err).NotTo(HaveOccurred(), "failed to create GCP HTTP client")
			gcpClient, err := compute.NewService(testCtx.Context, option.WithHTTPClient(httpClient))
			Expect(err).NotTo(HaveOccurred(), "failed to create GCP compute client")

			// Fetch the forwarding rule to get its VPC network URL.
			forwardingRule, err := gcpClient.ForwardingRules.Get(project, region, string(psc.Spec.ForwardingRuleName)).
				Context(testCtx.Context).Do()
			Expect(err).NotTo(HaveOccurred(),
				"failed to get ForwardingRule %s", psc.Spec.ForwardingRuleName)
			Expect(forwardingRule.Network).NotTo(BeEmpty(),
				"ForwardingRule %s should have a Network field", psc.Spec.ForwardingRuleName)

			// Fetch the NAT subnet to get its VPC network URL.
			subnet, err := gcpClient.Subnetworks.Get(project, region, string(psc.Spec.NATSubnet)).
				Context(testCtx.Context).Do()
			Expect(err).NotTo(HaveOccurred(),
				"failed to get Subnetwork %s", psc.Spec.NATSubnet)
			Expect(subnet.Network).NotTo(BeEmpty(),
				"Subnetwork %s should have a Network field", psc.Spec.NATSubnet)

			// The core assertion: the NAT subnet must be in the same VPC as the forwarding rule.
			Expect(subnet.Network).To(Equal(forwardingRule.Network),
				"NAT subnet %s (network %s) must be in the same VPC as forwarding rule %s (network %s)",
				psc.Spec.NATSubnet, subnet.Network, psc.Spec.ForwardingRuleName, forwardingRule.Network)
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