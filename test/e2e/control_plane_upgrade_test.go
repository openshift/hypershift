//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestUpgradeControlPlane(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	t.Logf("Starting control plane upgrade test. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := int32(int(clusterOpts.NodePoolReplicas) * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the first rollout to be complete
	t.Logf("Waiting for initial cluster rollout. Image: %s", globalOpts.PreviousReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, globalOpts.PreviousReleaseImage)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Update the cluster image
	t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	original := hostedCluster.DeepCopy()
	hostedCluster.Spec.Release.Image = globalOpts.LatestReleaseImage
	if hostedCluster.Annotations == nil {
		hostedCluster.Annotations = make(map[string]string)
	}
	hostedCluster.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
	err = client.Patch(ctx, hostedCluster, crclient.MergeFrom(original))
	g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

	// Wait for the new rollout to be complete
	t.Logf("waiting for updated cluster image rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available after upgrade")
	guestClient = e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	e2eutil.EnsureMachineDeploymentGeneration(t, ctx, client, hostedCluster, 1)
	// TODO (cewong): enable this test once the fix for KAS->Kubelet communication has merged
	// e2eutil.EnsureNodeCommunication(t, ctx, client, hostedCluster)
}
