//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

var (
	zeroReplicas int32 = 0
	oneReplicas  int32 = 1
)

func TestNodePool(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer func() {
		t.Log("Test: NodePool finished")
		cancel()
	}()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	mgmtClient, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// We set replicas to 0 in order to allow the inner tests to
	// create their own NodePools with the proper replicas
	clusterOpts.NodePoolReplicas = 0
	guestCluster := e2eutil.CreateCluster(t, ctx, mgmtClient, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)
	guestClient := e2eutil.WaitForGuestClient(t, ctx, mgmtClient, guestCluster)

	// Set of tests
	// Each test should have their own NodePool
	t.Run("TestNodePoolAutoRepair", testNodePoolAutoRepair(ctx, mgmtClient, guestCluster, guestClient, clusterOpts))
}

func nodePoolScaleDownToZero(ctx context.Context, client crclient.Client, nodePool hyperv1.NodePool, t *testing.T) {
	err := client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	np := nodePool.DeepCopy()
	nodePool.Spec.Replicas = &zeroReplicas
	if err = client.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Error(fmt.Errorf("failed to downscale nodePool %s: %v", nodePool.Name, err), "cannot scaledown the desired nodepool")
	}
}
