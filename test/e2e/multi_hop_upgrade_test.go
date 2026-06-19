//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMultiHopUpgrade(t *testing.T) {
	t.Parallel()

	imageChain := buildV1ReleaseImageChain()
	if len(imageChain) < 3 {
		t.Skipf("multi-hop upgrade requires at least 3 release images (got %d); set -e2e.n1-minor-release-image through -e2e.n3-minor-release-image", len(imageChain))
	}

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Multi-hop upgrade: %d hops planned across %d images", len(imageChain)-1, len(imageChain))
	for i, img := range imageChain {
		t.Logf("  image[%d]: %s", i, img)
	}

	// Set the release version to the starting image so version-gated condition
	// checks in ValidateHostedClusterConditions use the correct thresholds.
	if err := e2eutil.SetReleaseImageVersion(ctx, imageChain[0], globalOpts.ConfigurableClusterOptions.PullSecretFile); err != nil {
		t.Fatalf("failed to set release image version for starting image: %v", err)
	}

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = imageChain[0]
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		var startingVersion string
		if hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 {
			startingVersion = hostedCluster.Status.Version.History[0].Version
			t.Logf("Starting version: %s", startingVersion)
		}

		// Find the default NodePool created with the cluster.
		npList := &hyperv1.NodePoolList{}
		g.Expect(mgtClient.List(ctx, npList, crclient.InNamespace(hostedCluster.Namespace))).To(Succeed())
		var defaultNP *hyperv1.NodePool
		for i := range npList.Items {
			if npList.Items[i].Spec.ClusterName == hostedCluster.Name {
				defaultNP = &npList.Items[i]
				break
			}
		}
		g.Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist for multi-hop upgrade")

		upgradeTimeout := nodePoolUpgradeTimeout(hostedCluster.Spec.Platform.Type)

		for i := 1; i < len(imageChain); i++ {
			targetImage := imageChain[i]
			hopNum := i
			previousVersion := startingVersion

			t.Logf("=== Hop %d/%d: upgrading to %s ===", hopNum, len(imageChain)-1, targetImage)

			// Upgrade control plane.
			t.Run(fmt.Sprintf("Hop%d/ControlPlaneUpgrade", hopNum), func(t *testing.T) {
				err := e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
					obj.Spec.Release.Image = targetImage
					if obj.Annotations == nil {
						obj.Annotations = make(map[string]string)
					}
					obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = targetImage
				})
				g.Expect(err).NotTo(HaveOccurred(), "hop %d: failed to update hosted cluster release image", hopNum)

				e2eutil.WaitForControlPlaneComponentRollout(t, ctx, mgtClient, hostedCluster, previousVersion)
				e2eutil.WaitForControlPlaneRollout(t, ctx, mgtClient, hostedCluster)
				e2eutil.WaitForDataPlaneRollout(t, ctx, mgtClient, hostedCluster)
			})

			g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)).To(Succeed())
			if hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 {
				startingVersion = hostedCluster.Status.Version.History[0].Version
			}
			t.Logf("Hop %d: control plane upgraded to version %s", hopNum, startingVersion)

			// Upgrade NodePool.
			t.Run(fmt.Sprintf("Hop%d/NodePoolUpgrade", hopNum), func(t *testing.T) {
				g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), defaultNP)).To(Succeed())
				original := defaultNP.DeepCopy()
				defaultNP.Spec.Release.Image = targetImage
				g.Expect(mgtClient.Patch(ctx, defaultNP, crclient.MergeFrom(original))).To(Succeed(),
					"hop %d: failed to update NodePool release image", hopNum)

				e2eutil.EventuallyObject(t, ctx,
					fmt.Sprintf("NodePool %s/%s to start upgrading (hop %d)", defaultNP.Namespace, defaultNP.Name, hopNum),
					func(ctx context.Context) (*hyperv1.NodePool, error) {
						np := &hyperv1.NodePool{}
						err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)
						return np, err
					},
					[]e2eutil.Predicate[*hyperv1.NodePool]{
						e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
							Type:   hyperv1.NodePoolUpdatingVersionConditionType,
							Status: metav1.ConditionTrue,
						}),
					},
				)

				e2eutil.EventuallyObject(t, ctx,
					fmt.Sprintf("NodePool %s/%s to finish upgrading (hop %d)", defaultNP.Namespace, defaultNP.Name, hopNum),
					func(ctx context.Context) (*hyperv1.NodePool, error) {
						np := &hyperv1.NodePool{}
						err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)
						return np, err
					},
					[]e2eutil.Predicate[*hyperv1.NodePool]{
						e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
							Type:   hyperv1.NodePoolUpdatingVersionConditionType,
							Status: metav1.ConditionFalse,
						}),
					},
					e2eutil.WithTimeout(upgradeTimeout),
				)

				e2eutil.WaitForReadyNodesByNodePool(t, ctx, guestClient, defaultNP, hostedCluster.Spec.Platform.Type)
			})

			t.Logf("Hop %d: NodePool upgraded successfully", hopNum)
		}

		g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)).To(Succeed())
		if hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 {
			t.Logf("Multi-hop upgrade complete. Final version: %s", hostedCluster.Status.Version.History[0].Version)
		}

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mgtClient, guestClient, hostedCluster.Spec.Platform.Type, hostedCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgtClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "multi-hop-upgrade", globalOpts.ServiceAccountSigningKey)
}

func buildV1ReleaseImageChain() []string {
	images := []string{
		globalOpts.N3MinorReleaseImage,
		globalOpts.N2MinorReleaseImage,
		globalOpts.N1MinorReleaseImage,
		globalOpts.LatestReleaseImage,
	}

	var chain []string
	for _, img := range images {
		if img != "" {
			chain = append(chain, img)
		}
	}
	return chain
}

func nodePoolUpgradeTimeout(platform hyperv1.PlatformType) time.Duration {
	switch platform {
	case hyperv1.AzurePlatform, hyperv1.KubevirtPlatform:
		return 45 * time.Minute
	default:
		return 20 * time.Minute
	}
}
