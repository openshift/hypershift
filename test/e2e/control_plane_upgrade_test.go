//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestUpgradeControlPlane(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client := e2eutil.GetClientOrDie()

	t.Logf("Starting control plane upgrade test. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	clusterOpts := globalOpts.DefaultClusterOptions()

	hostedCluster := e2eutil.CreateAWSCluster(t, ctx, client, clusterOpts, globalOpts.ArtifactDir)

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err := client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)

	// Wait for the first rollout to be complete
	t.Logf("Waiting for initial cluster rollout. Image: %s", globalOpts.PreviousReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.PreviousReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Update the cluster image
	t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	hostedCluster.Spec.Release.Image = globalOpts.LatestReleaseImage
	err = client.Update(testContext, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

	// Wait for the new rollout to be complete
	t.Logf("waiting for updated cluster image rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, crclient.ObjectKeyFromObject(nodepool))
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}
