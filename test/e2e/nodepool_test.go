//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
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

	// Set of tests
	// Each test should have their own NodePool
	clusterOpts := globalOpts.DefaultClusterOptions(t)

	mgmtClient, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// We set replicas to 0 in order to allow the inner tests to
	// create their own NodePools with the proper replicas
	clusterOpts.NodePoolReplicas = 0
	guestCluster := e2eutil.CreateCluster(t, ctx, mgmtClient, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)
	guestClient := e2eutil.WaitForGuestClient(t, ctx, mgmtClient, guestCluster)

	t.Run("Refactored", func(t *testing.T) {
		t.Run("TestNodePoolAutoRepair", testNodePoolAutoRepair(ctx, mgmtClient, guestCluster, guestClient, clusterOpts))
		t.Run("TestNodepoolMachineconfigGetsRolledout", testNodepoolMachineconfigGetsRolledout(ctx, mgmtClient, guestCluster, guestClient, clusterOpts))
	})
}

// nodePoolScaleDownToZero function will scaleDown the nodePool created for the current tests
// when it finishes the execution. It's usually summoned via defer.
func nodePoolScaleDownToZero(ctx context.Context, client crclient.Client, nodePool hyperv1.NodePool, t *testing.T) {
	err := client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	np := nodePool.DeepCopy()
	nodePool.Spec.Replicas = &zeroReplicas
	if err = client.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Error(fmt.Errorf("failed to downscale nodePool %s: %v", nodePool.Name, err), "cannot scaledown the desired nodepool")
	}
}

// nodePoolRecreate function will recreate the NodePool if that exists during the E2E test execution.
// The situation should not happen in CI but it's useful in local testing.
func nodePoolRecreate(t *testing.T, ctx context.Context, nodePool *hyperv1.NodePool, mgmtClient crclient.Client) error {
	g := NewWithT(t)
	existantNodePool := &hyperv1.NodePool{}
	err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), existantNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
	err = mgmtClient.Delete(ctx, existantNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to Delete the existant NodePool")
	t.Logf("waiting for NodePools to be recreated")
	err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
		if ctx.Err() != nil {
			return false, err
		}
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				t.Logf("WARNING: NodePool still there, will retry")
				return false, nil
			}
			t.Logf("ERROR: Cannot create the NodePool, reason: %v", err)
			return false, nil
		}
		return true, nil
	})
	t.Logf("Nodepool Recreated")

	return nil
}
