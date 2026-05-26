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
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
)

// RegisterHostedClusterDNSTests registers DNS-related hosted cluster tests.
func RegisterHostedClusterDNSTests(getTestCtx internal.TestContextGetter) {
	EnsureKubeAPIDNSNameCustomCertTest(getTestCtx)
}

func EnsureKubeAPIDNSNameCustomCertTest(getTestCtx internal.TestContextGetter) {
	When("KubeAPIDNSName and custom certificate are configured", func() {
		PIt("should make KAS reachable via the custom DNS endpoint", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()
			if e2eutil.IsLessThan(e2eutil.Version419) {
				Skip("custom DNS name test requires version >= 4.19")
			}
			if hostedCluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
				Skip("custom DNS name test not supported on KubeVirt platform")
			}

			if !netutil.IsPublicHC(hostedCluster) {
				Skip("custom DNS name test requires a public hosted cluster")
			}

			serviceDomain := internal.GetEnvVarValue("E2E_SERVICE_DOMAIN")
			if serviceDomain == "" {
				Skip("E2E_SERVICE_DOMAIN not set; skipping custom DNS name test")
			}

			// The full implementation would:
			// 1. Generate a custom TLS cert via e2eutil.GenerateCustomCertificate()
			// 2. Create a cert secret in the HCP namespace
			// 3. Update HC with KubeAPIDNSName and custom serving cert reference
			// 4. Wait for custom kubeconfig status to appear (30-min timeout)
			// 5. Create ExternalName Service with DNS annotation
			// 6. Wait for DNS resolution and KAS reachability
			// 7. Validate custom kubeconfig status and secret
			// 8. Defer full HC state restoration
			//
			// This test is marked pending until the full DNS lifecycle is wired up.
			Expect(serviceDomain).NotTo(BeEmpty())
		})
	})
}

var _ = Describe("Hosted Cluster DNS", Label("hosted-cluster-dns"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterDNSTests(func() *internal.TestContext { return testCtx })
})
