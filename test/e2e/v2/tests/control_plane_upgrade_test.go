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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ControlPlaneUpgradeTest upgrades the hosted cluster from N-1 to the latest release image.
func ControlPlaneUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade the control plane from N-1 to latest", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())

		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		Expect(latestImage).NotTo(BeEmpty(), "E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")

		testCtx.SkipIfVersionBelow(e2eutil.Version422)

		var startingVersion string
		if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
			startingVersion = hc.Status.Version.History[0].Version
		}

		GinkgoWriter.Printf("Starting upgrade from version %s to image %s\n", startingVersion, latestImage)

		err = e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = latestImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = latestImage
		})
		Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster release image")

		By(fmt.Sprintf("Waiting for the control plane and data plane to reach image %s", latestImage))
		Eventually(func(g Gomega) {
			currentHC := &hyperv1.HostedCluster{}
			hcErr := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), currentHC)
			if hcErr != nil {
				g.Expect(hcErr).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", hc.Namespace, hc.Name)
				return
			}

			g.Expect(currentHC.Status.ControlPlaneVersion.Desired.Image).To(Equal(latestImage))
			if len(currentHC.Status.ControlPlaneVersion.History) == 0 {
				g.Expect(currentHC.Status.ControlPlaneVersion.History).NotTo(BeEmpty())
				return
			}
			g.Expect(currentHC.Status.ControlPlaneVersion.History[0].State).To(Equal(configv1.CompletedUpdate))

			if currentHC.Status.Version == nil {
				g.Expect(currentHC.Status.Version).NotTo(BeNil())
				return
			}
			g.Expect(currentHC.Status.Version.Desired.Image).To(Equal(latestImage))
			if len(currentHC.Status.Version.History) == 0 {
				g.Expect(currentHC.Status.Version.History).NotTo(BeEmpty())
				return
			}
			g.Expect(currentHC.Status.Version.History[0].State).To(Equal(configv1.CompletedUpdate))
		}).WithContext(ctx).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

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

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneUpgrade] Control Plane Upgrade", Label("lifecycle", "control-plane-upgrade"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterControlPlaneUpgradeTests(func() *internal.TestContext { return testCtx })
})
