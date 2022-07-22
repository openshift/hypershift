//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/version"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCreateClusterWithArmNodePool(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.Arch = "arm64"

	defaultVersion, err := version.LookupDefaultOCPVersion()
	if err != nil {
		t.Fail()
	}
	clusterOpts.ReleaseImage = defaultVersion.PullSpec
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", clusterOpts.ReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, clusterOpts.ReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}
