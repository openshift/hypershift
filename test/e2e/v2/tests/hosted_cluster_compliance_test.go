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

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterHostedClusterComplianceTests registers all hosted cluster compliance tests.
func RegisterHostedClusterComplianceTests(getTestCtx internal.TestContextGetter) {
	EnsureCustomLabelsTest(getTestCtx)
	EnsureCustomTolerationsTest(getTestCtx)
	EnsureAllRoutesUseHCPRouterTest(getTestCtx)
}

func EnsureCustomLabelsTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has custom labels configured", func() {
		It("should propagate labels to all control plane pods", Label("Informing"), func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version419) {
				Skip("custom labels propagation requires version >= 4.19")
			}

			podList := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, podList, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
			Expect(podList.Items).NotTo(BeEmpty(), "expected pods in control plane namespace %s", tc.ControlPlaneNamespace)

			var podsWithoutLabel []string
			for _, pod := range podList.Items {
				if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
					continue
				}
				if value, exist := pod.Labels["hypershift-e2e-test-label"]; !exist || value != "test" {
					podsWithoutLabel = append(podsWithoutLabel, pod.Name)
				}
			}
			Expect(podsWithoutLabel).To(BeEmpty(), "pods without custom label: %v", podsWithoutLabel)
		})
	})
}

func EnsureCustomTolerationsTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has custom tolerations configured", func() {
		It("should propagate tolerations to all control plane pods", Label("Informing"), func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version419) {
				Skip("custom tolerations propagation requires version >= 4.19")
			}

			podList := &corev1.PodList{}
			Expect(tc.MgmtClient.List(tc.Context, podList, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
			Expect(podList.Items).NotTo(BeEmpty(), "expected pods in control plane namespace %s", tc.ControlPlaneNamespace)

			var podsWithoutToleration []string
			for _, pod := range podList.Items {
				if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
					continue
				}
				found := false
				for _, t := range pod.Spec.Tolerations {
					if t.Key == "hypershift-e2e-test-toleration" &&
						t.Operator == corev1.TolerationOpEqual &&
						t.Value == "true" &&
						t.Effect == corev1.TaintEffectNoSchedule {
						found = true
						break
					}
				}
				if !found {
					podsWithoutToleration = append(podsWithoutToleration, pod.Name)
				}
			}
			Expect(podsWithoutToleration).To(BeEmpty(), "pods without custom toleration: %v", podsWithoutToleration)
		})
	})
}

func EnsureAllRoutesUseHCPRouterTest(getTestCtx internal.TestContextGetter) {
	When("routes are created in the control plane namespace", func() {
		It("should label all routes for the per-HCP router", func() {
			tc := getTestCtx()
			hostedCluster := tc.GetHostedCluster()

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

var _ = Describe("Hosted Cluster Compliance", Label("hosted-cluster-compliance"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterComplianceTests(func() *internal.TestContext { return testCtx })
})
