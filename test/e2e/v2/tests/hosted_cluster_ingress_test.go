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

	operatorv1 "github.com/openshift/api/operator/v1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"k8s.io/apimachinery/pkg/types"
)

func RegisterHostedClusterIngressTests(getTestCtx internal.TestContextGetter) {
	ValidateIngressOperatorConfigurationTest(getTestCtx)
}

func ValidateIngressOperatorConfigurationTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has IngressOperator EndpointPublishingStrategy configured", func() {
		It("should reflect the custom strategy in the hosted cluster IngressController", func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version421) {
				Skip("Ingress operator configuration requires version >= 4.21")
			}
			hc := tc.GetHostedCluster()

			if hc.Spec.OperatorConfiguration == nil ||
				hc.Spec.OperatorConfiguration.IngressOperator == nil ||
				hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy == nil {
				Skip("HostedCluster does not have IngressOperator EndpointPublishingStrategy configured")
			}

			expectedStrategy := hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy

			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

			Eventually(func(g Gomega) {
				ic := &operatorv1.IngressController{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: "openshift-ingress-operator",
					Name:      "default",
				}, ic)).To(Succeed(), "failed to get IngressController default in hosted cluster")

				g.Expect(ic.Spec.EndpointPublishingStrategy).NotTo(BeNil(),
					"IngressController EndpointPublishingStrategy should be set")
				g.Expect(ic.Spec.EndpointPublishingStrategy.Type).To(Equal(expectedStrategy.Type),
					fmt.Sprintf("expected EndpointPublishingStrategy type %s, got %s", expectedStrategy.Type, ic.Spec.EndpointPublishingStrategy.Type))
				if expectedStrategy.LoadBalancer != nil {
					g.Expect(ic.Spec.EndpointPublishingStrategy.LoadBalancer).NotTo(BeNil(),
						"IngressController LoadBalancer configuration should be set")
					g.Expect(ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope).To(Equal(expectedStrategy.LoadBalancer.Scope),
						fmt.Sprintf("expected LoadBalancer scope %s, got %s", expectedStrategy.LoadBalancer.Scope, ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope))
				}
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("Hosted Cluster Ingress", Label("hosted-cluster-ingress"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterIngressTests(func() *internal.TestContext { return testCtx })
})
