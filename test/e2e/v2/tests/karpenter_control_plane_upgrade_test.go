//go:build e2ev2

package tests

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func RegisterKarpenterControlPlaneUpgradeTests(getTestCtx internal.TestContextGetter) {
	KarpenterUpgradeTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift] Karpenter",
	Label("lifecycle", "karpenter-upgrade", internal.InformingLabel), Ordered, func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

			// Skips unless the Karpenter v1 API is available.
			// The v1 API exists on 4.23+, but when the operator is built from main and
			// tested against a 4.22 hosted cluster, set RUN_KARPENTER_TESTS=true to
			// lower the gate to 4.22.
			if internal.GetEnvVarValue("RUN_KARPENTER_TESTS") == "true" {
				testCtx.SkipIfVersionBelow(e2eutil.Version422)
			} else {
				testCtx.SkipIfVersionBelow(e2eutil.Version423)
			}
		})

		RegisterKarpenterControlPlaneUpgradeTests(func() *internal.TestContext { return testCtx })
	})

func KarpenterUpgradeTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] Karpenter Upgrade", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		// This test does not wait for CAPI worker NodePools. BaseNodePool and TestWorkload use a
		// dedicated taint and matching tolerations so platform workloads stay off these nodes,
		// which isolates Karpenter behavior on HyperShift. The test does not assert how many
		// Karpenter nodes exist; it only checks that a workload running on a pre-upgrade node
		// is rescheduled onto a replacement node after control plane upgrade and drift.
		It("should upgrade the control plane and drift Karpenter nodes to the new version", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
			Expect(latestImage).NotTo(BeEmpty(), "E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")
			previousImage := hc.Spec.Release.Image
			By("Starting Karpenter control plane upgrade")
			GinkgoWriter.Printf("FromImage: %s, toImage: %s\n", previousImage, latestImage)

			karpenterNodePool := v2util.BaseNodePool("on-demand", "default")
			// Disable consolidation so a drift replacement is not removed before the workload reschedules.
			karpenterNodePool.Spec.Disruption.ConsolidateAfter = karpenterv1.MustParseNillableDuration("Never")

			replicas := 1
			podLabels := map[string]string{"app": "web-app"}
			nodeLabels := map[string]string{
				karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
			}
			workload := v2util.TestWorkload("web-app", int32(replicas), nodeLabels)

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, karpenterNodePool, workload, nodeLabels, false)
			By("Waiting for workload to run on a Karpenter node")
			preUpgradePods := v2util.WaitForReadyKarpenterPods(Default, ctx, hcClient, nil, nil, replicas, podLabels)
			preUpgradeNode := &corev1.Node{}
			Expect(hcClient.Get(ctx, crclient.ObjectKey{Name: preUpgradePods[0].Spec.NodeName}, preUpgradeNode)).To(Succeed())
			logKarpenterNodeVersionInfo("pre-upgrade", preUpgradeNode, previousImage)

			By("Waiting for ready NodeClaim associated with pre-upgrade node")
			preUpgradeNodeClaim := waitForReadyNodeClaim(Default, ctx, hcClient, preUpgradeNode.Name, karpenterNodePool.Name)
			GinkgoWriter.Printf("Pre-upgrade NodeClaim: %s\n", preUpgradeNodeClaim.Name)

			By(fmt.Sprintf("Updating cluster release image to %s", latestImage))
			err = e2eutil.UpdateObject(t, ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Release.Image = latestImage
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = latestImage
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster release image")

			// Assert NO drift during CP upgrade. Unpinned NodeClaims should not detect
			// drift until the control plane upgrade completes, because the ignition config
			// hash is derived from the completed release image, not the desired one.
			By("Waiting for control plane rollout without premature NodeClaim drift")
			expectControlPlaneRolloutWithoutDrift(Default, ctx, tc.MgmtClient, hcClient, hc, latestImage, preUpgradeNodeClaim)
			GinkgoWriter.Println("Control plane upgrade completed")

			By("Waiting for pre-upgrade NodeClaim to be drifted after control plane upgrade")
			Eventually(func(g Gomega, pollCtx context.Context) {
				current := &karpenterv1.NodeClaim{}
				err := hcClient.Get(pollCtx, crclient.ObjectKeyFromObject(preUpgradeNodeClaim), current)
				if apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: NodeClaim %s not found; assuming it was deleted after drifting\n", preUpgradeNodeClaim.Name)
					return
				}
				g.Expect(err).NotTo(HaveOccurred())

				found := false
				for _, condition := range current.Status.Conditions {
					if condition.Type == karpenterv1.ConditionTypeDrifted {
						found = true
						g.Expect(condition.Status).To(Equal(metav1.ConditionTrue),
							"condition %s is not True in NodeClaim %s", karpenterv1.ConditionTypeDrifted, preUpgradeNodeClaim.Name)
					}
				}
				g.Expect(found).To(BeTrue(), "condition %s not found in NodeClaim %s", karpenterv1.ConditionTypeDrifted, preUpgradeNodeClaim.Name)
			}).
				WithContext(ctx).
				WithTimeout(5 * time.Minute).
				WithPolling(3 * time.Second).
				Should(Succeed())
			GinkgoWriter.Println("Pre-upgrade NodeClaim reported drifted")

			By("Waiting for workload to reschedule off the pre-upgrade node")
			newReadyPods := v2util.WaitForReadyKarpenterPods(Default, ctx, hcClient, nil, []corev1.Node{*preUpgradeNode}, replicas, podLabels)
			postUpgradeNode := &corev1.Node{}
			Expect(hcClient.Get(ctx, crclient.ObjectKey{Name: newReadyPods[0].Spec.NodeName}, postUpgradeNode)).To(Succeed())
			logKarpenterNodeVersionInfo("post-upgrade", postUpgradeNode, latestImage)
			GinkgoWriter.Printf("Workload rescheduled from node %s to node %s\n",
				preUpgradeNode.Name, postUpgradeNode.Name)

			By("Waiting for ready NodeClaim associated with replacement node")
			replacementNodeClaim := waitForReadyNodeClaim(Default, ctx, hcClient, postUpgradeNode.Name, karpenterNodePool.Name)
			GinkgoWriter.Printf("Replacement NodeClaim: %s\n", replacementNodeClaim.Name)

			By("Validating AutoNode status counts are populated after upgrade")
			Eventually(func(g Gomega, pollCtx context.Context) {
				updated := &hyperv1.HostedCluster{}
				err := tc.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(hc), updated)
				g.Expect(err).NotTo(HaveOccurred())
				if err != nil {
					return
				}

				g.Expect(updated.Status.AutoNode.NodeCount).NotTo(BeNil())
				if updated.Status.AutoNode.NodeCount == nil {
					return
				}
				g.Expect(*updated.Status.AutoNode.NodeCount).To(BeNumerically(">=", replicas))

				g.Expect(updated.Status.AutoNode.NodeClaimCount).NotTo(BeNil())
				if updated.Status.AutoNode.NodeClaimCount == nil {
					return
				}
				g.Expect(*updated.Status.AutoNode.NodeClaimCount).To(BeNumerically(">=", replicas))
			}).
				WithContext(ctx).
				WithTimeout(5 * time.Minute).
				WithPolling(3 * time.Second).
				Should(Succeed())
		})
	})
}

// logKarpenterNodeVersionInfo logs the version information of a Karpenter node for debugging and observability purposes.
// It's possible that both the kubeletVersion and the OSImage don't change (but underlying ignition config does).
// Therefore, don't assert that they should differ after an upgrade, but simply log for observability.
func logKarpenterNodeVersionInfo(testPhase string, node *corev1.Node, releaseImage string) {
	GinkgoHelper()
	info := node.Status.NodeInfo
	GinkgoWriter.Printf("%s node: %s\n", testPhase, node.Name)
	GinkgoWriter.Printf("  releaseImage:      %s\n", releaseImage)
	GinkgoWriter.Printf("  kubelet:           %s\n", info.KubeletVersion)
	GinkgoWriter.Printf("  osImage:           %s\n", info.OSImage)
}

// waitForReadyNodeClaim polls until a non-deleting, Initialized NodeClaim exists for the
// given node and NodePool name.
func waitForReadyNodeClaim(g Gomega, ctx context.Context, client crclient.Client, nodeName, nodePoolName string) *karpenterv1.NodeClaim {
	GinkgoHelper()
	var matched *karpenterv1.NodeClaim
	g.Eventually(func(pollG Gomega, pollCtx context.Context) {
		all := &karpenterv1.NodeClaimList{}
		pollG.Expect(client.List(pollCtx, all)).To(Succeed())
		for i := range all.Items {
			claim := &all.Items[i]
			if claim.Status.NodeName != nodeName {
				continue
			}
			if claim.Labels[karpenterv1.NodePoolLabelKey] != nodePoolName {
				continue
			}
			if !claim.DeletionTimestamp.IsZero() {
				continue
			}
			ready := false
			for _, c := range claim.Status.Conditions {
				if c.Type == karpenterv1.ConditionTypeInitialized && c.Status == metav1.ConditionTrue {
					ready = true
					break
				}
			}
			pollG.Expect(ready).To(BeTrue(), "NodeClaim %s for node %s is not ready", claim.Name, nodeName)
			matched = claim
			return
		}
		pollG.Expect(matched).NotTo(BeNil(), "no ready NodeClaim for node %s in NodePool %s", nodeName, nodePoolName)
	}).
		WithContext(ctx).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
	return matched
}

// expectControlPlaneRolloutWithoutDrift waits for ControlPlaneVersion to reach
// Completed for the target image while asserting that the pre-upgrade NodeClaim
// does not drift or get deleted before the rollout finishes. If the NodeClaim shows
// premature drift or disappears, the failure is permanent (StopTrying).
func expectControlPlaneRolloutWithoutDrift(
	g Gomega,
	ctx context.Context,
	mgmtClient crclient.Client,
	guestClient crclient.Client,
	hc *hyperv1.HostedCluster,
	targetImage string,
	nodeClaim *karpenterv1.NodeClaim,
) {
	GinkgoHelper()

	// TODO(maxcao13): Drift is only a failure while the NodeClass is gated from setting userData + AMI.
	// This test infers that from mgmt ControlPlaneVersion, which can lag the operator ungating EC2NodeClass
	// spec. An UpgradePending condition on the NodeClass set in the same reconcile as NodeClass.spec
	// freeze/unfreeze would remove that race. We may have to go with that path if tests show to be flaky.

	g.Eventually(func(pollG Gomega) {
		// Check CP completion first. If the target image already reached Completed,
		// drift is expected and correct, so return success without inspecting NodeClaims.
		currentHC := &hyperv1.HostedCluster{}
		err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), currentHC)
		pollG.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", hc.Namespace, hc.Name)

		cpv := currentHC.Status.ControlPlaneVersion
		if cpv.Desired.Image == targetImage &&
			len(cpv.History) > 0 &&
			cpv.History[0].Image == targetImage &&
			cpv.History[0].State == configv1.CompletedUpdate {
			return
		}

		// CP rollout still in progress — any drift at this point is premature.
		nc := &karpenterv1.NodeClaim{}
		err = guestClient.Get(ctx, crclient.ObjectKeyFromObject(nodeClaim), nc)
		if apierrors.IsNotFound(err) {
			StopTrying(fmt.Sprintf("NodeClaim %s deleted before CP upgrade completed", nodeClaim.Name)).Now()
		}
		pollG.Expect(err).NotTo(HaveOccurred(), "failed to get NodeClaim %s", nodeClaim.Name)

		conditions, err := e2eutil.Conditions(nc)
		pollG.Expect(err).NotTo(HaveOccurred(), "failed to read conditions for NodeClaim %s", nodeClaim.Name)
		for _, c := range conditions {
			if c.Type == karpenterv1.ConditionTypeDrifted && c.Status == metav1.ConditionTrue {
				StopTrying(fmt.Sprintf("NodeClaim %s detected drift before CP upgrade completed", nodeClaim.Name)).Now()
			}
		}

		// Signal that CP is not yet complete so Eventually retries. History[0] may
		// still be the previous Completed rollout right after Desired moves to
		// targetImage, so require History[0].Image to match as well.
		pollG.Expect(cpv.Desired.Image).To(Equal(targetImage))
		pollG.Expect(cpv.History).NotTo(BeEmpty(), "controlPlaneVersion has no history yet")
		pollG.Expect(cpv.History[0].Image).To(Equal(targetImage), "waiting for target image in controlPlaneVersion history")
		pollG.Expect(cpv.History[0].State).To(Equal(configv1.CompletedUpdate))
	}).
		WithContext(ctx).
		WithTimeout(30 * time.Minute).
		WithPolling(15 * time.Second).
		Should(Succeed())
}
