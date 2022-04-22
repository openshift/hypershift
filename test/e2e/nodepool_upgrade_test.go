//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestUpgradeNodePool(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Starting nodepool upgrade test from %s to %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions()
	clusterOpts.ReleaseImage = globalOpts.LatestReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch v := o.(type) {
		case *hyperv1.NodePool:
			v.Spec.Release.Image = globalOpts.PreviousReleaseImage
			v.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
				Strategy: hyperv1.UpgradeStrategyRollingUpdate,
				RollingUpdate: &hyperv1.RollingUpdate{
					MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
					MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(3)),
				},
			}
		}
	}

	// Look up metadata about the release images so that we can extract the version
	// information for later assertions
	releaseInfoProvider := &releaseinfo.RegistryClientProvider{}
	pullSecretFile, err := os.Open(clusterOpts.PullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to open pull secret file")
	defer pullSecretFile.Close()
	pullSecret, err := ioutil.ReadAll(pullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to read pull secret file")
	previousReleaseInfo, err := releaseInfoProvider.Lookup(ctx, globalOpts.PreviousReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for previous image")
	latestReleaseInfo, err := releaseInfoProvider.Lookup(ctx, globalOpts.LatestReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for latest image")

	// Create the test cluster
	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Wait for connectivity to the cluster
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes)

	// Wait for the first rollout to be complete and refresh the hostedcluster
	t.Logf("Waiting for initial cluster rollout. Image: %s", hostedCluster.Spec.Release.Image)
	e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, hostedCluster.Spec.Release.Image)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Find nodepools
	var nodePools hyperv1.NodePoolList
	err = client.List(ctx, &nodePools, &crclient.ListOptions{Namespace: hostedCluster.Namespace})
	g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools")

	// Wait for nodepools to roll out the initial version
	// TODO: Consider doing this in parallel
	for _, nodePool := range nodePools.Items {
		e2eutil.WaitForNodePoolVersion(t, ctx, client, &nodePool, previousReleaseInfo.Version())
	}

	// Update nodepool images to the latest
	for _, nodePool := range nodePools.Items {
		err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
		t.Logf("Updating nodepool image. Image: %s", globalOpts.LatestReleaseImage)
		nodePool.Spec.Release.Image = globalOpts.LatestReleaseImage
		err = client.Update(ctx, &nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed update nodePool image")
	}

	// Wait for Nodes to be Ready
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes)

	// Wait for nodepools to roll out the latest version
	// TODO: Consider doing this in parallel
	for _, nodePool := range nodePools.Items {
		e2eutil.WaitForNodePoolVersion(t, ctx, client, &nodePool, latestReleaseInfo.Version())
		e2eutil.WaitForNodePoolConditionsNotToBePresent(t, ctx, client, &nodePool, hyperv1.NodePoolUpdatingVersionConditionType)
	}

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, client, hostedCluster)
	e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, client, hostedCluster)
}
