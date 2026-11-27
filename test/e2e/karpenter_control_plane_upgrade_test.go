//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/blang/semver"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/supportedversion"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestKarpenterUpgradeControlPlane(t *testing.T) {
	e2eutil.ShouldRunKarpenterTests(t)
	if globalOpts.Platform != hyperv1.AWSPlatform && globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("test only supported on platforms AWS and Azure")
	}
	if globalOpts.Platform == hyperv1.AzurePlatform && !azureutil.IsAroHCP() {
		t.Skip("test only supported on Azure with ARO HCP")
	}
	if globalOpts.Platform == hyperv1.AzurePlatform && globalOpts.ConfigurableClusterOptions.AzureKarpenterClientID == "" {
		t.Skip("Azure Karpenter tests require --e2e.azure-karpenter-client-id")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	switch globalOpts.Platform {
	case hyperv1.AWSPlatform:
		clusterOpts.AWSPlatform.AutoNode = true
		clusterOpts.AWSPlatform.PublicOnly = false
		clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)
	case hyperv1.AzurePlatform:
		clusterOpts.AzurePlatform.AutoNode = true
	}
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		replicas := 1
		newNodePoolAndWorkload := func(nodePoolName, nodeClassName, workloadName string) (*karpenterv1.NodePool, *appsv1.Deployment) {
			nodePool := baseNodePool(nodePoolName, nodeClassName, hostedCluster.Spec.Platform.Type)
			testTaint := corev1.Taint{Key: "hypershift.io/test", Value: "karpenter-test", Effect: corev1.TaintEffectNoSchedule}
			nodePool.Spec.Template.Spec.Taints = []corev1.Taint{testTaint}
			nodePool.Spec.Disruption.ConsolidateAfter = karpenterv1.MustParseNillableDuration("Never")
			nodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: nodePool.Name}
			workload := testWorkload(workloadName, int32(replicas), nodeLabels)
			workload.Spec.Template.Spec.Tolerations = []corev1.Toleration{{
				Key:      testTaint.Key,
				Operator: corev1.TolerationOpEqual,
				Value:    testTaint.Value,
				Effect:   testTaint.Effect,
			}}
			return nodePool, workload
		}
		karpenterNodePool, workLoads := newNodePoolAndWorkload("on-demand", "default", "web-app")
		nodeLabels := workLoads.Spec.Template.Spec.NodeSelector
		podLabels := workLoads.Spec.Selector.MatchLabels

		t.Logf("Starting Karpenter control plane upgrade. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

		// Control plane upgrades should replace unpinned workers, but must not drift
		// or replace workers pinned to an explicit version. Use a separate
		// NodeClass to check both behaviors in the same upgrade.
		g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)).To(Succeed())
		cpVersion, err := semver.Parse(hostedCluster.Status.ControlPlaneVersion.Desired.Version)
		g.Expect(err).NotTo(HaveOccurred(), "failed to parse control plane version")
		// Use n-2 because n-1 may not be published in Cincinnati during branching.
		prevMajor, prevMinor, err := supportedversion.PreviousMinorVersion(cpVersion, 2)
		g.Expect(err).NotTo(HaveOccurred(), "failed to select a released NodeClass version")
		pinnedVersion := fmt.Sprintf("%d.%d.0", prevMajor, prevMinor)
		t.Logf("Creating a separate NodeClass pinned to released version %s", pinnedVersion)
		pinnedNodeClass, err := e2eutil.CreateKarpenterNodeClass(ctx, guestClient, hostedCluster.Spec.Platform.Type, "pinned-upgrade", pinnedVersion)
		g.Expect(err).NotTo(HaveOccurred())
		t.Cleanup(func() {
			g.Expect(crclient.IgnoreNotFound(guestClient.Delete(ctx, pinnedNodeClass))).To(Succeed())
		})
		pinnedNodePool, pinnedWorkload := newNodePoolAndWorkload("pinned-upgrade", pinnedNodeClass.GetName(), "pinned-web-app")
		pinnedNodeLabels := pinnedWorkload.Spec.Template.Spec.NodeSelector
		pinnedPodLabels := pinnedWorkload.Spec.Selector.MatchLabels
		defer guestClient.Delete(ctx, karpenterNodePool)
		defer guestClient.Delete(ctx, workLoads)
		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads")

		g.Expect(guestClient.Create(ctx, pinnedNodePool)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(crclient.IgnoreNotFound(guestClient.Delete(ctx, pinnedNodePool))).To(Succeed())
			e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, pinnedNodeLabels)
		})
		g.Expect(guestClient.Create(ctx, pinnedWorkload)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(crclient.IgnoreNotFound(guestClient.Delete(ctx, pinnedWorkload))).To(Succeed())
		})

		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)
		nodeClaims := waitForReadyNodeClaims(t, ctx, guestClient, len(nodes), crclient.MatchingLabels(nodeLabels))
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas, podLabels, "")

		preUpgradeNodeName := nodes[0].Name
		t.Logf("Pre-upgrade node: %s", preUpgradeNodeName)

		pinnedNodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), pinnedNodeLabels)
		pinnedNodeClaims := waitForReadyNodeClaims(t, ctx, guestClient, len(pinnedNodes), crclient.MatchingLabels(pinnedNodeLabels))
		waitForReadyKarpenterPods(t, ctx, guestClient, pinnedNodes, replicas, pinnedPodLabels, "")
		g.Expect(pinnedNodeClaims.Items[0].Status.NodeName).To(Equal(pinnedNodes[0].Name))
		checkPinnedNode := func(g Gomega, pollCtx context.Context) {
			g.Expect(e2eutil.CheckKarpenterNodeUnchanged(pollCtx, guestClient, &pinnedNodes[0], &pinnedNodeClaims.Items[0])).To(Succeed())
		}

		t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
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
		allNodeClaims := nodeClaims.DeepCopy()
		allNodeClaims.Items = append(allNodeClaims.Items, pinnedNodeClaims.Items...)
		expectControlPlaneRolloutWithoutDrift(t, ctx, mgtClient, guestClient, hostedCluster, globalOpts.LatestReleaseImage, allNodeClaims)
		checkPinnedNode(g, ctx)

		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// After CP upgrade completes, drift should now be detected.
		t.Logf("Control plane upgrade complete, waiting for NodeClaim drift detection")
		// Count drift detection and replacement readiness toward the five-minute window.
		pinnedCheckDeadline := time.Now().Add(5 * time.Minute)
		for i := range nodeClaims.Items {
			waitForNodeClaimDrifted(t, ctx, guestClient, &nodeClaims.Items[i])
		}
		t.Logf("Karpenter Nodes drifted")

		t.Logf("Waiting for workloads to reschedule off pre-upgrade node %s", preUpgradeNodeName)
		newReadyPods := waitForReadyKarpenterPods(t, ctx, guestClient, nil, replicas, podLabels, preUpgradeNodeName)

		g.Expect(len(newReadyPods)).To(Equal(replicas), "expected %d new ready pods, got %d", replicas, len(newReadyPods))
		postUpgradeNodeName := newReadyPods[0].Spec.NodeName
		t.Logf("Workload successfully rescheduled from node: %s to node: %s", preUpgradeNodeName, postUpgradeNodeName)

		t.Log("Checking that pinned nodes do not drift or get replaced after the unpinned nodes upgrade")
		checkPinnedNode(g, ctx)
		if remaining := time.Until(pinnedCheckDeadline); remaining > 0 {
			g.Consistently(checkPinnedNode).
				WithContext(ctx).
				WithTimeout(remaining).
				WithPolling(3 * time.Second).
				Should(Succeed())
		}
		waitForReadyKarpenterPods(t, ctx, guestClient, pinnedNodes, replicas, pinnedPodLabels, "")

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
					if *hc.Status.AutoNode.NodeCount < int32(replicas) {
						return false, fmt.Sprintf("expected NodeCount >= %d, got %v", replicas, hc.Status.AutoNode.NodeCount), nil
					}
					if hc.Status.AutoNode.NodeClaimCount == nil || *hc.Status.AutoNode.NodeClaimCount < int32(replicas) {
						return false, fmt.Sprintf("expected NodeClaimCount >= %d, got %v", replicas, hc.Status.AutoNode.NodeClaimCount), nil
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
	}).WithUpgradeTarget(globalOpts.LatestReleaseImage).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter-cp-upgrade", globalOpts.ServiceAccountSigningKey)
}
