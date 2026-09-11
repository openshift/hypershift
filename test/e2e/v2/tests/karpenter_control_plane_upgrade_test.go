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
			GinkgoWriter.Printf("Starting Karpenter control plane upgrade. FromImage: %s, toImage: %s\n", previousImage, latestImage)

			// TODO(maxcao13): On 0-worker AutoNode clusters, platform workloads can still scale Karpenter
			// beyond one NodeClaim. Follow-up: baseline drift checks from NodeClaims for this Karpenter
			// NodePool (stable count), not a single node.
			hypershiftNodePoolList := &hyperv1.NodePoolList{}
			Expect(tc.MgmtClient.List(ctx, hypershiftNodePoolList, crclient.InNamespace(hc.Namespace))).To(Succeed())
			hasWorkerNodePool := false
			for i := range hypershiftNodePoolList.Items {
				np := &hypershiftNodePoolList.Items[i]
				if np.Spec.ClusterName != hc.Name {
					continue
				}
				if np.Spec.Replicas != nil && *np.Spec.Replicas > 0 {
					hasWorkerNodePool = true
					break
				}
			}
			if !hasWorkerNodePool {
				// TODO(maxcao13): We should not skip and support fixing this test when there are zero CAPI workloads
				// The fix should be that don't prescribe exactly how many Karpenter NodeClaim/Nodes are expected,
				// rather that a workload that was scheduled onto a Karpenter Node, was rescheduled onto and runs on some new Karpenter Node.
				Skip("no Hypershift NodePools with replicas > 0; upgrade test requires CAPI workers until multi-NodeClaim baseline is implemented")
			}

			By("Waiting for Hypershift NodePool workers to be ready on the hosted cluster")
			for i := range hypershiftNodePoolList.Items {
				np := &hypershiftNodePoolList.Items[i]
				if np.Spec.ClusterName != hc.Name {
					continue
				}
				if np.Spec.Replicas != nil && *np.Spec.Replicas > 0 {
					e2eutil.WaitForReadyNodesByNodePool(t, ctx, hcClient, np, hc.Spec.Platform.Type)
				}
			}

			karpenterNodePool := baseNodePool("on-demand", "default")
			// TODO(maxcao13): We disable consolidation as a hack to prevent flakiness in this blocking test.
			// Erroneous consolidation can cause the test to fail where the new Node is consolidated due to Empty or
			// Underutilized before the old node's pods get scheduled to it. The proper fix should come from upstream
			// Karpenter's disruption ordering/budgeting logic. Ref: https://redhat.atlassian.net/browse/OCPBUGS-91966
			karpenterNodePool.Spec.Disruption.ConsolidateAfter = karpenterv1.MustParseNillableDuration("Never")

			replicas := 1
			nodeLabels := map[string]string{
				karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
			}
			workLoads := testWorkload("web-app", int32(replicas), nodeLabels)

			By("Creating Karpenter NodePool and workloads")
			Expect(hcClient.Create(ctx, karpenterNodePool)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, karpenterNodePool); err != nil {
					if !apierrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", karpenterNodePool.Name)
					}
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, nodeLabels)
			})

			By("Waiting for Karpenter NodePool to be ready")
			Eventually(func(g Gomega, pollCtx context.Context) {
				np := &karpenterv1.NodePool{}
				g.Expect(hcClient.Get(pollCtx, crclient.ObjectKeyFromObject(karpenterNodePool), np)).To(Succeed())
				for _, want := range []string{karpenterv1.ConditionTypeValidationSucceeded, karpenterv1.ConditionTypeNodeClassReady} {
					var found bool
					for _, c := range np.Status.Conditions {
						if c.Type == want {
							found = true
							g.Expect(c.Status).To(Equal(metav1.ConditionTrue), "NodePool %s condition %s", np.Name, want)
						}
					}
					g.Expect(found).To(BeTrue(), "NodePool %s missing condition %s", np.Name, want)
				}
			}).
				WithContext(ctx).
				WithTimeout(5 * time.Minute).
				WithPolling(3 * time.Second).
				Should(Succeed())

			Expect(hcClient.Create(ctx, workLoads)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, workLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", workLoads.Name)
				}
			})
			GinkgoWriter.Println("Created workloads")

			By("Waiting for Karpenter nodes and pods to be ready")
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, int32(replicas), nodeLabels)
			nodeClaims := waitForReadyNodeClaims(ctx, hcClient, len(nodes), nil, true)
			waitForReadyKarpenterPods(ctx, hcClient, nodes, nil, replicas, map[string]string{"app": "web-app"})

			preUpgradeNode := nodes[0]
			GinkgoWriter.Printf("Pre-upgrade node: %s\n", preUpgradeNode.Name)

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
			By("Waiting for CP rollout and asserting no premature drift")
			expectControlPlaneRolloutWithoutDrift(ctx, tc.MgmtClient, hcClient, hc, latestImage, nodeClaims)
			GinkgoWriter.Println("Control plane upgraded")

			// By this point, there should only be 1 NodeClaim we are waiting for to drift.
			By("Waiting for NodeClaims to be drifted after CP upgrade")
			for i := range nodeClaims.Items {
				nodeClaim := &nodeClaims.Items[i]
				Eventually(func(g Gomega, pollCtx context.Context) {
					current := &karpenterv1.NodeClaim{}
					err := hcClient.Get(pollCtx, crclient.ObjectKeyFromObject(nodeClaim), current)
					if apierrors.IsNotFound(err) {
						GinkgoWriter.Printf("WARNING: NodeClaim %s not found; assuming it was deleted after drifting\n", nodeClaim.Name)
						return
					}
					g.Expect(err).NotTo(HaveOccurred())
					if err != nil {
						return
					}

					found := false
					for _, condition := range current.Status.Conditions {
						if condition.Type == karpenterv1.ConditionTypeDrifted {
							found = true
							g.Expect(condition.Status).To(Equal(metav1.ConditionTrue),
								"condition %s is not True in NodeClaim %s", karpenterv1.ConditionTypeDrifted, nodeClaim.Name)
						}
					}
					g.Expect(found).To(BeTrue(), "condition %s not found in NodeClaim %s", karpenterv1.ConditionTypeDrifted, nodeClaim.Name)
				}).
					WithContext(ctx).
					WithTimeout(5 * time.Minute).
					WithPolling(3 * time.Second).
					Should(Succeed())
			}
			GinkgoWriter.Println("Karpenter Nodes drifted")

			By("Waiting for replacement NodeClaims to be ready")
			replacementNodeClaims := waitForReadyNodeClaims(ctx, hcClient, replicas, nodeClaims.Items, false)
			Expect(replacementNodeClaims.Items).NotTo(BeEmpty(), "no replacement NodeClaims found")

			// Collect all the associated Nodes for the replacement NodeClaims.
			replacementNodes := make([]corev1.Node, 0, len(replacementNodeClaims.Items))
			for i := range replacementNodeClaims.Items {
				claim := &replacementNodeClaims.Items[i]
				Expect(claim.Status.NodeName).NotTo(BeEmpty(), "replacement NodeClaim %s has no nodeName", claim.Name)
				Expect(claim.Status.NodeName).NotTo(Equal(preUpgradeNode.Name),
					"replacement NodeClaim %s has unexpected associated node: %s", claim.Name, claim.Status.NodeName)

				node := &corev1.Node{}
				Expect(hcClient.Get(ctx, crclient.ObjectKey{Name: claim.Status.NodeName}, node)).To(Succeed())
				replacementNodes = append(replacementNodes, *node)
				GinkgoWriter.Printf("Replacement node: %s\n", node.Name)
			}

			// Make sure that the workloads are actually rescheduled on the replacement nodes.
			By("Waiting for workloads to schedule on replacement nodes")
			newReadyPods := waitForReadyKarpenterPods(ctx, hcClient, replacementNodes, []corev1.Node{preUpgradeNode}, replicas, map[string]string{"app": "web-app"})

			Expect(len(newReadyPods)).To(Equal(replicas), "expected %d new ready pods, got %d", replicas, len(newReadyPods))
			postUpgradeNodeName := newReadyPods[0].Spec.NodeName
			GinkgoWriter.Printf("Workload successfully rescheduled from node: %s to node: %s\n", preUpgradeNode.Name, postUpgradeNodeName)

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

// waitForReadyNodeClaims polls until n non-excluded NodeClaims are ready.
// When strict is true, exactly n ready non-excluded NodeClaims are required.
// When strict is false, at least n ready non-excluded NodeClaims are required.
func waitForReadyNodeClaims(ctx context.Context, client crclient.Client, n int, excludeNodeClaims []karpenterv1.NodeClaim, strict bool) *karpenterv1.NodeClaimList {
	GinkgoHelper()
	nodeClaims := &karpenterv1.NodeClaimList{}
	matched := &karpenterv1.NodeClaimList{}

	Eventually(func(g Gomega, pollCtx context.Context) {
		err := client.List(pollCtx, nodeClaims)
		g.Expect(err).NotTo(HaveOccurred(), "failed to list NodeClaims")

		candidates := make([]karpenterv1.NodeClaim, 0, len(nodeClaims.Items))
		for i := range nodeClaims.Items {
			claim := nodeClaims.Items[i]
			if !claim.DeletionTimestamp.IsZero() {
				continue
			}
			excluded := false
			for _, excludedClaim := range excludeNodeClaims {
				if claim.Name == excludedClaim.Name {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			candidates = append(candidates, claim)
		}

		readyCandidates := make([]karpenterv1.NodeClaim, 0, len(candidates))
		for i := range candidates {
			claim := &candidates[i]
			hasLaunched := false
			hasRegistered := false
			hasInitialized := false
			for _, condition := range claim.Status.Conditions {
				if condition.Type == karpenterv1.ConditionTypeLaunched && condition.Status == metav1.ConditionTrue {
					hasLaunched = true
				}
				if condition.Type == karpenterv1.ConditionTypeRegistered && condition.Status == metav1.ConditionTrue {
					hasRegistered = true
				}
				if condition.Type == karpenterv1.ConditionTypeInitialized && condition.Status == metav1.ConditionTrue {
					hasInitialized = true
				}
			}
			if !hasLaunched || !hasRegistered || !hasInitialized {
				continue
			}
			readyCandidates = append(readyCandidates, *claim)
		}

		if strict {
			g.Expect(readyCandidates).To(HaveLen(n),
				"expected exactly %d ready non-excluded NodeClaims, got %d", n, len(readyCandidates))
		} else {
			g.Expect(len(readyCandidates)).To(BeNumerically(">=", n),
				"expected at least %d ready non-excluded NodeClaims, got %d", n, len(readyCandidates))
		}

		matched.Items = readyCandidates
		if len(matched.Items) > n {
			matched.Items = matched.Items[:n]
		}
	}).
		WithContext(ctx).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	return matched
}

// expectControlPlaneRolloutWithoutDrift waits for ControlPlaneVersion to reach
// Completed for the target image while asserting that no pre-upgrade NodeClaim
// drifts or is deleted before the rollout finishes. If any NodeClaim shows
// premature drift or disappears, the failure is permanent (StopTrying).
func expectControlPlaneRolloutWithoutDrift(
	ctx context.Context,
	mgmtClient crclient.Client,
	guestClient crclient.Client,
	hc *hyperv1.HostedCluster,
	targetImage string,
	nodeClaims *karpenterv1.NodeClaimList,
) {
	GinkgoHelper()

	// TODO(maxcao13): Drift is only a failure while the NodeClass is gated from setting userData + AMI.
	// This test infers that from mgmt ControlPlaneVersion, which can lag the operator ungating EC2NodeClass
	// spec. An UpgradePending condition on the NodeClass set in the same reconcile as NodeClass.spec
	// freeze/unfreeze would remove that race. We may have to go with that path if tests show to be flaky.

	Eventually(func(g Gomega) {
		// Check CP completion first. If the target image already reached Completed,
		// drift is expected and correct, so return success without inspecting NodeClaims.
		currentHC := &hyperv1.HostedCluster{}
		err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), currentHC)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", hc.Namespace, hc.Name)

		cpv := currentHC.Status.ControlPlaneVersion
		if cpv.Desired.Image == targetImage &&
			len(cpv.History) > 0 &&
			cpv.History[0].Image == targetImage &&
			cpv.History[0].State == configv1.CompletedUpdate {
			return
		}

		// CP rollout still in progress — any drift at this point is premature.
		for i := range nodeClaims.Items {
			nc := &karpenterv1.NodeClaim{}
			err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(&nodeClaims.Items[i]), nc)
			if apierrors.IsNotFound(err) {
				StopTrying(fmt.Sprintf("NodeClaim %s deleted before CP upgrade completed", nodeClaims.Items[i].Name)).Now()
			}
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodeClaim %s", nodeClaims.Items[i].Name)

			conditions, err := e2eutil.Conditions(nc)
			g.Expect(err).NotTo(HaveOccurred(), "failed to read conditions for NodeClaim %s", nodeClaims.Items[i].Name)
			for _, c := range conditions {
				if c.Type == karpenterv1.ConditionTypeDrifted && c.Status == metav1.ConditionTrue {
					StopTrying(fmt.Sprintf("NodeClaim %s detected drift before CP upgrade completed", nodeClaims.Items[i].Name)).Now()
				}
			}
		}

		// Signal that CP is not yet complete so Eventually retries. History[0] may
		// still be the previous Completed rollout right after Desired moves to
		// targetImage, so require History[0].Image to match as well.
		g.Expect(cpv.Desired.Image).To(Equal(targetImage))
		g.Expect(cpv.History).NotTo(BeEmpty(), "controlPlaneVersion has no history yet")
		g.Expect(cpv.History[0].Image).To(Equal(targetImage), "waiting for target image in controlPlaneVersion history")
		g.Expect(cpv.History[0].State).To(Equal(configv1.CompletedUpdate))
	}).
		WithContext(ctx).
		WithTimeout(30 * time.Minute).
		WithPolling(15 * time.Second).
		Should(Succeed())
}
