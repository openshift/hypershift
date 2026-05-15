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
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ControlPlaneUpgradeTest upgrades the hosted cluster from N-1 to the latest release image.
func ControlPlaneUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade the control plane from N-1 to latest", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		hc := testCtx.GetHostedCluster()
		Expect(hc).NotTo(BeNil(), "hosted cluster should be available")

		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		Expect(latestImage).NotTo(BeEmpty(), "E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")

		var startingVersion string
		if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
			startingVersion = hc.Status.Version.History[0].Version
		}
		GinkgoWriter.Printf("Starting upgrade from version %s to image %s\n", startingVersion, latestImage)

		err := e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = latestImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = latestImage
		})
		Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster release image")

		By("Waiting for control plane components to complete rollout")
		e2eutil.GinkgoAtLeast(e2eutil.Version420)
		e2eutil.WaitForControlPlaneComponentRollout(GinkgoTB(), ctx, testCtx.MgmtClient, hc, startingVersion)

		By("Waiting for control plane version to complete rollout")
		e2eutil.GinkgoAtLeast(e2eutil.Version422)
		e2eutil.WaitForControlPlaneRollout(GinkgoTB(), ctx, testCtx.MgmtClient, hc)

		By("Waiting for data plane rollout to complete")
		e2eutil.WaitForDataPlaneRollout(GinkgoTB(), ctx, testCtx.MgmtClient, hc)

		// Re-fetch HC after upgrade
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())

		// TODO: Add post-upgrade validation checks once the Ensure* functions
		// in e2eutil are refactored from *testing.T to testing.TB:
		//   - EnsureFeatureGateStatus
		//   - EnsureNodeCountMatchesNodePoolReplicas
		//   - EnsureNoCrashingPods
		//   - EnsureMachineDeploymentGeneration
	})
}

// RegisterControlPlaneUpgradeTests registers all control plane upgrade tests.
func RegisterControlPlaneUpgradeTests(getTestCtx internal.TestContextGetter) {
	ControlPlaneUpgradeTest(getTestCtx)
}

var _ = Describe("Control Plane Upgrade", Label("control-plane-upgrade"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterControlPlaneUpgradeTests(func() *internal.TestContext { return testCtx })
})
