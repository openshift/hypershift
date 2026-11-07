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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/util/deterministiclb"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:DeterministicLBIP] Hosted Cluster Deterministic LoadBalancer IP", Label("lifecycle", "deterministic-lb-ip", internal.InformingLabel), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		for _, name := range []string{
			"HYPERSHIFT_DETERMINISTIC_LB_EIP",
			"HYPERSHIFT_DETERMINISTIC_LB_SUBNET",
			"E2E_EXPECTED_DETERMINISTIC_LB_IP",
		} {
			if internal.GetEnvVarValue(name) == "" {
				Skip(fmt.Sprintf("deterministic LB EIP pre-step did not provide %s", name))
			}
		}
	})

	It("should configure the primary kube-apiserver LoadBalancer Service from the HostedCluster annotations", func() {
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred(), "read the HostedCluster")

		err = deterministiclb.Check(deterministiclb.Options{
			Context:               testCtx.Context,
			Client:                testCtx.MgmtClient,
			HostedCluster:         hc,
			ControlPlaneNamespace: testCtx.ControlPlaneNamespace,
			ExpectedIP:            internal.GetEnvVarValue("E2E_EXPECTED_DETERMINISTIC_LB_IP"),
			Timeout:               15 * time.Minute,
			Out:                   GinkgoWriter,
			GuestClient: func(_ context.Context, fresh *hyperv1.HostedCluster) (crclient.Client, error) {
				return testCtx.GetHostedClusterClient(fresh)
			},
		})
		Expect(err).NotTo(HaveOccurred(), "deterministic LB IP checks")
	})
})
