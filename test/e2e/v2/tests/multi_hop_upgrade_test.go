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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// MultiHopUpgradeTest sequentially upgrades a HostedCluster's control plane
// and NodePool through multiple minor versions from the oldest available
// release image to the latest.
func MultiHopUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade control plane and NodePool through multiple minor versions", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()
		ctx := testCtx.Context
		hc := testCtx.GetHostedCluster()
		hcClient := testCtx.GetHostedClusterClient()

		e2eutil.GinkgoAtLeast(e2eutil.Version420)

		imageChain := buildReleaseImageChain()
		if len(imageChain) < 3 {
			Skip(fmt.Sprintf("multi-hop upgrade requires at least 3 release images (got %d); set E2E_N1_RELEASE_IMAGE through E2E_N3_RELEASE_IMAGE", len(imageChain)))
		}

		var startingVersion string
		if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
			startingVersion = hc.Status.Version.History[0].Version
		}
		GinkgoWriter.Printf("Multi-hop upgrade: %d hops planned, starting from version %s\n", len(imageChain)-1, startingVersion)

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist for multi-hop upgrade")

		upgradeTimeout := nodePoolUpgradeTimeout(hc.Spec.Platform.Type)

		for i := 1; i < len(imageChain); i++ {
			targetImage := imageChain[i]
			hopNum := i
			previousVersion := startingVersion

			By(fmt.Sprintf("Hop %d/%d: upgrading control plane", hopNum, len(imageChain)-1))

			err := e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Release.Image = targetImage
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = targetImage
			})
			Expect(err).NotTo(HaveOccurred(), "hop %d: failed to update hosted cluster release image", hopNum)

			By(fmt.Sprintf("Hop %d/%d: waiting for control plane component rollout", hopNum, len(imageChain)-1))
			e2eutil.WaitForControlPlaneComponentRollout(GinkgoTB(), ctx, testCtx.MgmtClient, hc, previousVersion)

			By(fmt.Sprintf("Hop %d/%d: waiting for control plane version rollout", hopNum, len(imageChain)-1))
			e2eutil.WaitForControlPlaneRollout(GinkgoTB(), ctx, testCtx.MgmtClient, hc)

			By(fmt.Sprintf("Hop %d/%d: waiting for data plane rollout", hopNum, len(imageChain)-1))
			e2eutil.WaitForDataPlaneRollout(GinkgoTB(), ctx, testCtx.MgmtClient, hc)

			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
			if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
				startingVersion = hc.Status.Version.History[0].Version
			}
			GinkgoWriter.Printf("Hop %d: control plane upgraded to version %s\n", hopNum, startingVersion)

			By(fmt.Sprintf("Hop %d/%d: upgrading NodePool", hopNum, len(imageChain)-1))

			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), defaultNP)).To(Succeed())
			Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, func(obj *hyperv1.NodePool) {
				obj.Spec.Release.Image = targetImage
			})).To(Succeed(), "hop %d: failed to update NodePool release image", hopNum)

			Eventually(func(g Gomega) {
				np := &hyperv1.NodePool{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)).To(Succeed())
				internal.ValidateConditions(g, np, []e2eutil.Condition{
					{Type: hyperv1.NodePoolUpdatingVersionConditionType, Status: metav1.ConditionTrue},
				})
			}).WithTimeout(upgradeTimeout).WithPolling(10*time.Second).Should(Succeed(),
				"hop %d: NodePool should start upgrading", hopNum)

			Eventually(func(g Gomega) {
				np := &hyperv1.NodePool{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)).To(Succeed())
				internal.ValidateConditions(g, np, []e2eutil.Condition{
					{Type: hyperv1.NodePoolUpdatingVersionConditionType, Status: metav1.ConditionFalse},
				})
			}).WithTimeout(upgradeTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"hop %d: NodePool should finish upgrading", hopNum)

			e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, defaultNP, hc.Spec.Platform.Type)

			By(fmt.Sprintf("Hop %d/%d: verifying node health", hopNum, len(imageChain)-1))
			Eventually(func(g Gomega) {
				np := &hyperv1.NodePool{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)).To(Succeed())
				internal.ValidateConditions(g, np, []e2eutil.Condition{
					{Type: hyperv1.NodePoolAllMachinesReadyConditionType, Status: metav1.ConditionTrue},
					{Type: hyperv1.NodePoolAllNodesHealthyConditionType, Status: metav1.ConditionTrue},
				})
			}).WithTimeout(upgradeTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"hop %d: NodePool nodes should be healthy", hopNum)
			GinkgoWriter.Printf("Hop %d: NodePool upgraded successfully\n", hopNum)
		}

		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
			GinkgoWriter.Printf("Multi-hop upgrade complete. Final version: %s\n", hc.Status.Version.History[0].Version)
		}
	})
}

// buildReleaseImageChain returns an ordered list of release images from oldest
// to latest based on available E2E_N* environment variables. Empty entries are
// skipped so the chain adapts to however many images CI provides.
func buildReleaseImageChain() []string {
	envVars := []string{
		"E2E_N3_RELEASE_IMAGE",
		"E2E_N2_RELEASE_IMAGE",
		"E2E_N1_RELEASE_IMAGE",
		"E2E_LATEST_RELEASE_IMAGE",
	}

	var chain []string
	for _, envVar := range envVars {
		image := internal.GetEnvVarValue(envVar)
		if image != "" {
			chain = append(chain, image)
		}
	}
	return chain
}

// RegisterMultiHopUpgradeTests registers all multi-hop upgrade tests.
func RegisterMultiHopUpgradeTests(getTestCtx internal.TestContextGetter) {
	MultiHopUpgradeTest(getTestCtx)
}

var _ = Describe("Multi-Hop Upgrade", Label("lifecycle", "multi-hop-upgrade"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterMultiHopUpgradeTests(func() *internal.TestContext { return testCtx })
})
