//go:build e2ev2

package tests

import (
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

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
			GinkgoWriter.Println("Created Karpenter NodePool")

			Expect(hcClient.Create(ctx, workLoads)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, workLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", workLoads.Name)
				}
			})
			GinkgoWriter.Println("Created workloads")

			By("Waiting for Karpenter nodes and pods to be ready")
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, int32(replicas), nodeLabels)
			nodeClaims := waitForReadyNodeClaims(ctx, hcClient, len(nodes))
			waitForReadyKarpenterPods(ctx, hcClient, nodes, replicas, map[string]string{"app": "web-app"})

			preUpgradeOSImage := nodes[0].Status.NodeInfo.OSImage
			GinkgoWriter.Printf("Pre-upgrade node: %s, OS image: %s\n", nodes[0].Name, preUpgradeOSImage)

			By(fmt.Sprintf("Updating cluster release image to %s", latestImage))
			err = e2eutil.UpdateObject(t, ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Release.Image = latestImage
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = latestImage
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster release image")

			By("Waiting for NodeClaims to be drifted")
			for i := range nodeClaims.Items {
				nodeClaim := &nodeClaims.Items[i]
				Eventually(func(g Gomega, pollCtx context.Context) {
					current := &karpenterv1.NodeClaim{}
					err := hcClient.Get(pollCtx, crclient.ObjectKeyFromObject(nodeClaim), current)
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

			By("Waiting for control plane components to complete rollout")
			ExpectHostedClusterUpgradeToComplete(ctx, tc.MgmtClient, hc, latestImage)
			GinkgoWriter.Println("Control plane upgraded")

			preUpgradeRHCOSVersion := extractRHCOSVersion(preUpgradeOSImage)
			GinkgoWriter.Printf("Pre-upgrade RHCOS version: %s\n", preUpgradeRHCOSVersion)

			By("Waiting for replacement nodes with updated RHCOS version")
			nodes = e2eutil.WaitForNReadyNodesWithOptions(t, ctx, hcClient, int32(replicas), hyperv1.AWSPlatform, "",
				e2eutil.WithClientOptions(
					crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))},
				),
				e2eutil.WithPredicates(
					e2eutil.ConditionPredicate[*corev1.Node](e2eutil.Condition{
						Type:   string(corev1.NodeReady),
						Status: metav1.ConditionTrue,
					}),
					func(node *corev1.Node) (done bool, reasons string, err error) {
						postVersion := extractRHCOSVersion(node.Status.NodeInfo.OSImage)
						if postVersion == "" {
							return false, fmt.Sprintf("could not extract RHCOS version from %q", node.Status.NodeInfo.OSImage), nil
						}
						if postVersion < preUpgradeRHCOSVersion {
							return false, fmt.Sprintf("post-upgrade RHCOS %q is older than pre-upgrade %q", postVersion, preUpgradeRHCOSVersion), nil
						}
						return true, fmt.Sprintf("post-upgrade RHCOS %q is not older than pre-upgrade %q", postVersion, preUpgradeRHCOSVersion), nil
					},
				),
			)

			By("Waiting for Karpenter pods to schedule on the new nodes")
			waitForReadyKarpenterPods(ctx, hcClient, nodes, replicas, map[string]string{"app": "web-app"})

			nodeClaims = waitForReadyNodeClaims(ctx, hcClient, len(nodes))

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
				g.Expect(*updated.Status.AutoNode.NodeCount).To(BeNumerically(">=", len(nodes)))

				g.Expect(updated.Status.AutoNode.NodeClaimCount).NotTo(BeNil())
				if updated.Status.AutoNode.NodeClaimCount == nil {
					return
				}
				g.Expect(*updated.Status.AutoNode.NodeClaimCount).To(BeNumerically(">=", len(nodeClaims.Items)))
			}).
				WithContext(ctx).
				WithTimeout(5 * time.Minute).
				WithPolling(3 * time.Second).
				Should(Succeed())
		})
	})
}

var rhcosVersionRe = regexp.MustCompile(`Red Hat Enterprise Linux CoreOS (\d+\.\d+\.\d{8}-\d+)`)

func extractRHCOSVersion(osImage string) string {
	matches := rhcosVersionRe.FindStringSubmatch(osImage)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// waitForReadyNodeClaims polls until exactly n NodeClaims are present and all
// have Launched, Registered, and Initialized conditions set to True.
func waitForReadyNodeClaims(ctx context.Context, client crclient.Client, n int) *karpenterv1.NodeClaimList {
	t := GinkgoTB()
	nodeClaims := &karpenterv1.NodeClaimList{}
	e2eutil.EventuallyObjects(t, ctx, "NodeClaims to be ready",
		func(ctx context.Context) ([]*karpenterv1.NodeClaim, error) {
			err := client.List(ctx, nodeClaims)
			if err != nil {
				return nil, err
			}
			items := make([]*karpenterv1.NodeClaim, 0, len(nodeClaims.Items))
			for i := range nodeClaims.Items {
				items = append(items, &nodeClaims.Items[i])
			}
			return items, nil
		},
		[]e2eutil.Predicate[[]*karpenterv1.NodeClaim]{
			func(claims []*karpenterv1.NodeClaim) (done bool, reasons string, err error) {
				want, got := n, len(claims)
				return want == got, fmt.Sprintf("expected %d NodeClaims, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*karpenterv1.NodeClaim]{
			func(claim *karpenterv1.NodeClaim) (done bool, reasons string, err error) {
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
					return false, fmt.Sprintf("NodeClaim %s not ready: Launched=%v, Registered=%v, Initialized=%v",
						claim.Name, hasLaunched, hasRegistered, hasInitialized), nil
				}
				return true, "", nil
			},
		},
		e2eutil.WithTimeout(5*time.Minute),
	)

	return nodeClaims
}
