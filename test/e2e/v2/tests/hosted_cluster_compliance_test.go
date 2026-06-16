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
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	routev1 "github.com/openshift/api/route/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterHostedClusterComplianceTests registers all hosted cluster compliance tests.
func RegisterHostedClusterComplianceTests(getTestCtx internal.TestContextGetter) {
	EnsureAllRoutesUseHCPRouterTest(getTestCtx)
}

func EnsureAllRoutesUseHCPRouterTest(getTestCtx internal.TestContextGetter) {
	When("routes are created in the control plane namespace", func() {
		It("should label all routes for the per-HCP router", Label("routes"), func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()
			Expect(hostedCluster).NotTo(BeNil(), "hosted cluster must be configured")

			isRoute := false
			for _, svc := range hostedCluster.Spec.Services {
				if svc.Service == hyperv1.APIServer && svc.Type == hyperv1.Route {
					isRoute = true
					break
				}
			}
			if !isRoute {
				Skip("route test only applies when APIServer is exposed via Route")
			}

			routeList := &routev1.RouteList{}
			Expect(tc.MgmtClient.List(tc.Context, routeList, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
			Expect(routeList.Items).NotTo(BeEmpty(),
				"expected at least one route in control plane namespace %s", tc.ControlPlaneNamespace)

			for i := range routeList.Items {
				route := &routeList.Items[i]
				original := route.DeepCopy()
				netutil.AddHCPRouteLabel(route)
				Expect(route.Labels).To(Equal(original.Labels),
					"route %s is missing the label to use the per-HCP router", route.Name)
			}
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Compliance] Hosted Cluster Compliance", Label("hosted-cluster-compliance"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterComplianceTests(func() *internal.TestContext { return testCtx })
})
