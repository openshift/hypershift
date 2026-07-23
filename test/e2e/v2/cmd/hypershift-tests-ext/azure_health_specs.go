//go:build e2ev2

// azure_health_specs.go is a copy of ValidateConfigurationStatusTest from
// tests/hosted_cluster_health_test.go adapted for the OTE binary. The original
// file is untouched — this is a backward-compatible prototype to validate the
// OTE migration path.

package main

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func validateConfigurationStatusTest(getTestCtx internal.TestContextGetter) {
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
		Expect(testCtx).NotTo(BeNil(), "test context must be initialized")

		testCtx.ValidateHostedCluster()
	})

	validateConfigurationStatusTest(func() *internal.TestContext { return testCtx })
})
