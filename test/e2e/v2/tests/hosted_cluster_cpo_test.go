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

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterCPOTests(getTestCtx internal.TestContextGetter) {
	VerifyCPOOverrideImageTest(getTestCtx)
}

func VerifyCPOOverrideImageTest(getTestCtx internal.TestContextGetter) {
	When("a CPO override image is configured for the platform and version", func() {
		It("should run the control-plane-operator pod with the expected override image", func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()

			releaseImage := hc.Spec.Release.Image
			Expect(releaseImage).NotTo(BeEmpty(), "HostedCluster release image should be set")

			version := e2eutil.ExtractVersionFromReleaseImage(releaseImage)
			if version == "" {
				Skip(fmt.Sprintf("could not extract version from release image %s", releaseImage))
			}

			platform := string(hc.Spec.Platform.Type)
			expectedImage := controlplaneoperatoroverrides.CPOImage(platform, version)
			if expectedImage == "" {
				Skip(fmt.Sprintf("no CPO override configured for platform %s and version %s", platform, version))
			}

			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(tc.MgmtClient.List(tc.Context, podList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": "control-plane-operator"},
				)).To(Succeed(), "failed to list control-plane-operator pods")

				g.Expect(podList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-operator pod in namespace %s", tc.ControlPlaneNamespace)

				var runningPod *corev1.Pod
				for i := range podList.Items {
					if podList.Items[i].Status.Phase == corev1.PodRunning {
						runningPod = &podList.Items[i]
						break
					}
				}
				g.Expect(runningPod).NotTo(BeNil(),
					"expected at least one running control-plane-operator pod in namespace %s", tc.ControlPlaneNamespace)
				g.Expect(runningPod.Spec.Containers).NotTo(BeEmpty(),
					"control-plane-operator pod %s should have at least one container", runningPod.Name)
				g.Expect(runningPod.Spec.Containers[0].Image).To(Equal(expectedImage),
					"control-plane-operator pod %s container image should match override (expected %s, got %s)",
					runningPod.Name, expectedImage, runningPod.Spec.Containers[0].Image)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("Hosted Cluster CPO", Label("hosted-cluster-cpo"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterCPOTests(func() *internal.TestContext { return testCtx })
})
