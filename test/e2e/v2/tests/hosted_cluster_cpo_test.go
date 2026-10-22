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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterCPOTests(getTestCtx internal.TestContextGetter) {
	VerifyCPOOverrideImageTest(getTestCtx)
}

func VerifyCPOOverrideImageTest(getTestCtx internal.TestContextGetter) {
	registerCPOOverrideImageTest(getTestCtx, os.Getenv("TEST_CPO_OVERRIDE") == "1")
}

func registerCPOOverrideImageTest(getTestCtx internal.TestContextGetter, required bool) {
	When("a CPO override image is configured for the platform and version", func() {
		It("should run the control-plane-operator pod with the expected override image", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			releaseImage := hc.Spec.Release.Image
			Expect(releaseImage).NotTo(BeEmpty(), "HostedCluster release image should be set")

			Expect(hc.Status.Version).NotTo(BeNil(), "HostedCluster must report its release version")
			Expect(hc.Status.Version.History).NotTo(BeEmpty())
			version := hc.Status.Version.History[0].Version
			Expect(version).NotTo(BeEmpty())

			platform := string(hc.Spec.Platform.Type)
			expectedImage, err := cpoOverrideImageForTest(platform, version, required)
			Expect(err).NotTo(HaveOccurred(), "explicit CPO override selection must use a mapped release")
			if !required && expectedImage == "" {
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

				foundRunning := false
				foundExpectedImage := false
				for i := range podList.Items {
					pod := &podList.Items[i]
					if pod.Status.Phase != corev1.PodRunning {
						continue
					}
					foundRunning = true
					for _, c := range pod.Spec.Containers {
						if c.Name == "control-plane-operator" && c.Image == expectedImage {
							foundExpectedImage = true
							break
						}
					}
					if foundExpectedImage {
						break
					}
				}
				g.Expect(foundRunning).To(BeTrue(),
					"expected at least one running control-plane-operator pod in namespace %s", tc.ControlPlaneNamespace)
				g.Expect(foundExpectedImage).To(BeTrue(),
					"expected a running control-plane-operator container with image %s in namespace %s",
					expectedImage, tc.ControlPlaneNamespace)
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

func cpoOverrideImageForTest(platform, version string, required bool) (string, error) {
	image := controlplaneoperatoroverrides.CPOImage(platform, version)
	if required && image == "" {
		return "", fmt.Errorf("required CPO override test needs a mapped CPO image for platform %s release %s", platform, version)
	}
	return image, nil
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneOperator] Hosted Cluster CPO", Label("hosted-cluster-cpo"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterCPOTests(func() *internal.TestContext { return testCtx })
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneOperator] Required Hosted Cluster CPO", Label("hosted-cluster-cpo-required"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	registerCPOOverrideImageTest(func() *internal.TestContext { return testCtx }, true)
})
