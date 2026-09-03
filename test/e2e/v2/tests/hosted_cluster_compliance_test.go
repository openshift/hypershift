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
	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterHostedClusterComplianceTests registers all hosted cluster compliance tests.
func RegisterHostedClusterComplianceTests(getTestCtx internal.TestContextGetter) {
	EnsureAllRoutesUseHCPRouterTest(getTestCtx)
	EnsureKASConnectionCheckerSpecTest(getTestCtx)
}

func EnsureAllRoutesUseHCPRouterTest(getTestCtx internal.TestContextGetter) {
	When("routes are created in the control plane namespace", func() {
		It("should label all routes for the per-HCP router", Label("routes"), func() {
			tc := getTestCtx()
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

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

// EnsureKASConnectionCheckerSpecTest verifies that the kas-connection-checker
// deployment in the hosted cluster has the cluster-autoscaler safe-to-evict
// annotation, no custom tolerations, and a topology spread constraint.
//
// The test connects to the hosted cluster's kube-apiserver via port-forward
// rather than the external endpoint. This ensures the test works for every
// provider and visibility mode (public, private, publicAndPrivate), even
// when the external API endpoint is temporarily unreachable — for example,
// after Azure endpoint access transition tests cycle the topology.
func EnsureKASConnectionCheckerSpecTest(getTestCtx internal.TestContextGetter) {
	When("kas-connection-checker deployment is reconciled", func() {
		It("should have safe-to-evict annotation, no tolerations, and topology spread constraint", func() {
			tc := getTestCtx()

			if !tc.VersionAtLeast(e2eutil.Version423) {
				Skip("kas-connection-checker spec changes require CPO >= 4.23")
			}

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			pfc, err := tc.GetHostedClusterClientViaPortForward(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to establish port-forward to kube-apiserver")
			DeferCleanup(pfc.Close)

			dep := &appsv1.Deployment{}
			err = pfc.Get(tc.Context, crclient.ObjectKey{
				Namespace: hccomanifests.KASConnectionCheckerNamespace,
				Name:      hccomanifests.KASConnectionCheckerName,
			}, dep)
			Expect(err).NotTo(HaveOccurred(), "failed to get kas-connection-checker deployment")

			annotations := dep.Spec.Template.Annotations
			Expect(annotations).To(HaveKeyWithValue(
				"cluster-autoscaler.kubernetes.io/safe-to-evict", "true",
			), "kas-connection-checker pod template should have safe-to-evict annotation")

			Expect(dep.Spec.Template.Spec.Tolerations).To(BeEmpty(),
				"kas-connection-checker pod template should have no custom tolerations")

			Expect(dep.Spec.Template.Spec.TopologySpreadConstraints).To(HaveLen(1),
				"kas-connection-checker should have exactly one topology spread constraint")
			tsc := dep.Spec.Template.Spec.TopologySpreadConstraints[0]
			Expect(tsc.MaxSkew).To(Equal(int32(1)))
			Expect(tsc.TopologyKey).To(Equal("kubernetes.io/hostname"))
			Expect(tsc.WhenUnsatisfiable).To(Equal(corev1.ScheduleAnyway))
			Expect(tsc.LabelSelector).NotTo(BeNil(), "topology spread constraint should have a label selector")
			Expect(tsc.LabelSelector.MatchLabels).To(HaveKeyWithValue("app", "kas-connection-checker"))
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Compliance] Hosted Cluster Compliance", Label("hosted-cluster-compliance"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterComplianceTests(func() *internal.TestContext { return testCtx })
})
