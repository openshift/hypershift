//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestKarpenterUpgradeControlPlane(t *testing.T) {
	e2eutil.ShouldRunKarpenterTests(t)
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.AutoNode = true
	clusterOpts.AWSPlatform.PublicOnly = false
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

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

		t.Logf("Starting Karpenter control plane upgrade. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

		defer guestClient.Delete(ctx, karpenterNodePool)
		defer guestClient.Delete(ctx, workLoads)
		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads")

		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)
		nodeClaims := waitForReadyNodeClaims(t, ctx, guestClient, len(nodes))
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas, map[string]string{"app": "web-app"})

		preUpgradeOSImage := nodes[0].Status.NodeInfo.OSImage
		t.Logf("Pre-upgrade node: %s, OS image: %s", nodes[0].Name, preUpgradeOSImage)

		t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
		err := e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = globalOpts.LatestReleaseImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
			if globalOpts.DisablePKIReconciliation {
				obj.Annotations[hyperv1.DisablePKIReconciliationAnnotation] = "true"
			}
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

		// Assert NO drift during CP upgrade. Unpinned NodeClaims should not detect
		// drift until the control plane upgrade completes, because the ignition config
		// hash is derived from the completed release image, not the desired one.
		noDriftCancel, noDriftDone := assertNodeClaimsNotDrifted(t, ctx, guestClient, nodeClaims)

		e2eutil.WaitForImageRollout(t, ctx, mgtClient, hostedCluster)
		noDriftCancel()
		<-noDriftDone

		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// After CP upgrade completes, drift should now be detected.
		t.Logf("Control plane upgrade complete, waiting for NodeClaim drift detection")
		for _, nodeClaim := range nodeClaims.Items {
			waitForNodeClaimDrifted(t, ctx, guestClient, &nodeClaim)
		}
		t.Logf("Karpenter Nodes drifted")

		preUpgradeRHCOSVersion := extractRHCOSVersion(preUpgradeOSImage)
		t.Logf("Pre-upgrade RHCOS version: %s", preUpgradeRHCOSVersion)

		nodes = e2eutil.WaitForNReadyNodesWithOptions(t, ctx, guestClient, int32(replicas), hyperv1.AWSPlatform, "",
			e2eutil.WithClientOptions(
				crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))},
			),
			e2eutil.WithPredicates(
				e2eutil.ConditionPredicate[*corev1.Node](e2eutil.Condition{
					Type:   string(corev1.NodeReady),
					Status: metav1.ConditionTrue,
				}),
				e2eutil.Predicate[*corev1.Node](func(node *corev1.Node) (done bool, reasons string, err error) {
					postVersion := extractRHCOSVersion(node.Status.NodeInfo.OSImage)
					if postVersion == "" {
						return false, fmt.Sprintf("could not extract RHCOS version from %q", node.Status.NodeInfo.OSImage), nil
					}
					if postVersion < preUpgradeRHCOSVersion {
						return false, fmt.Sprintf("post-upgrade RHCOS %q is older than pre-upgrade %q", postVersion, preUpgradeRHCOSVersion), nil
					}
					return true, fmt.Sprintf("post-upgrade RHCOS %q is not older than pre-upgrade %q", postVersion, preUpgradeRHCOSVersion), nil
				}),
			),
		)

		t.Logf("Waiting for Karpenter pods to schedule on the new node")
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas, map[string]string{"app": "web-app"})

		nodeClaims = waitForReadyNodeClaims(t, ctx, guestClient, len(nodes))

		t.Log("Validating AutoNode status counts are populated after upgrade")
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNode status counts", hostedCluster.Namespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				return hc, err
			},
			[]e2eutil.Predicate[*hyperv1.HostedCluster]{
				func(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
					if hc.Status.AutoNode.NodeCount == nil {
						return false, "Status.AutoNode.NodeCount is nil", nil
					}
					if *hc.Status.AutoNode.NodeCount < int32(len(nodes)) {
						return false, fmt.Sprintf("expected NodeCount >= %d, got %v", len(nodes), hc.Status.AutoNode.NodeCount), nil
					}
					if hc.Status.AutoNode.NodeClaimCount == nil || *hc.Status.AutoNode.NodeClaimCount < int32(len(nodeClaims.Items)) {
						return false, fmt.Sprintf("expected NodeClaimCount >= %d, got %v", len(nodeClaims.Items), hc.Status.AutoNode.NodeClaimCount), nil
					}
					return true, fmt.Sprintf("AutoNode status: NodeCount=%d, NodeClaimCount=%d",
						*hc.Status.AutoNode.NodeCount, *hc.Status.AutoNode.NodeClaimCount), nil
				},
			},
			e2eutil.WithTimeout(5*time.Minute),
		)

		g.Expect(guestClient.Delete(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Deleted Karpenter NodePool")
		g.Expect(guestClient.Delete(ctx, workLoads)).To(Succeed())
		t.Logf("Delete workloads")

		t.Logf("Waiting for Karpenter Nodes to disappear")
		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, nodeLabels)
	}).WithUpgradeTarget(globalOpts.LatestReleaseImage).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter-upgrade-control-plane", globalOpts.ServiceAccountSigningKey)
}

var rhcosVersionRe = regexp.MustCompile(`Red Hat Enterprise Linux CoreOS (\d+\.\d+\.\d{8}-\d+)`)

func extractRHCOSVersion(osImage string) string {
	matches := rhcosVersionRe.FindStringSubmatch(osImage)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}
