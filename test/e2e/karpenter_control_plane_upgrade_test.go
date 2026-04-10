//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestKarpenterUpgradeControlPlane(t *testing.T) {
	e2eutil.AtLeast(t, e2eutil.Version419)
	if os.Getenv("TECH_PREVIEW_NO_UPGRADE") != "true" {
		t.Skipf("Only tested when CI sets TECH_PREVIEW_NO_UPGRADE=true and the Hypershift Operator is installed with --tech-preview-no-upgrade")
	}
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
		replicas := 1
		nodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
		}
		workLoads := testWorkload("web-app", int32(replicas), nodeLabels)

		t.Logf("Starting Karpenter control plane upgrade. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

		releaseProvider := &releaseinfo.RegistryClientProvider{}
		pullSecret, err := os.ReadFile(clusterOpts.PullSecretFile)
		g.Expect(err).NotTo(HaveOccurred())

		latestReleaseImage, err := releaseProvider.Lookup(ctx, globalOpts.LatestReleaseImage, pullSecret)
		g.Expect(err).NotTo(HaveOccurred())

		expectedRHCOSVersions := machineOSVersions(latestReleaseImage)
		g.Expect(expectedRHCOSVersions).NotTo(BeEmpty())
		t.Logf("machine-os version(s) in latest release: %v", expectedRHCOSVersions)

		defer guestClient.Delete(ctx, karpenterNodePool)
		defer guestClient.Delete(ctx, workLoads)
		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads")

		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)
		nodeClaims := waitForReadyNodeClaims(t, ctx, guestClient, len(nodes))
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

		preUpgradeOSImage := nodes[0].Status.NodeInfo.OSImage
		t.Logf("Pre-upgrade node: %s, OS image: %s", nodes[0].Name, preUpgradeOSImage)

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

		driftChan := make(chan struct{})
		go func() {
			defer close(driftChan)
			for _, nodeClaim := range nodeClaims.Items {
				waitForNodeClaimDrifted(t, ctx, guestClient, &nodeClaim)
			}
		}()

		e2eutil.WaitForImageRollout(t, ctx, mgtClient, hostedCluster)
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		<-driftChan
		t.Logf("Karpenter Nodes drifted")

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
					fullOSImageString := node.Status.NodeInfo.OSImage

					for _, v := range expectedRHCOSVersions {
						if strings.Contains(fullOSImageString, v) {
							return true, fmt.Sprintf("node OS image %q contains expected version %q", fullOSImageString, v), nil
						}
					}

					return false, fmt.Sprintf("expected node OS image %q to contain one of %v", fullOSImageString, expectedRHCOSVersions), nil
				}),
			),
		)

		t.Logf("Waiting for Karpenter pods to schedule on the new node")
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

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
