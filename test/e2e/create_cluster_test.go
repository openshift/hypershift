//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

// TestAWSCreateCluster implements a test that mimics the operation described in the
// HyperShift quick start (creating a basic guest cluster).
//
// This test is meant to provide a first, fast signal to detect regression; it
// is recommended to use it as a PR blocker test.
func TestAWSCreateCluster(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client := e2eutil.GetClientOrDie()

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
	t.Logf("Created nodepool. Namespace: %s, name: %s", nodepool.Namespace, nodepool.Name)

	// Wait for nodes to report ready
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, crclient.ObjectKeyFromObject(nodepool))
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client := e2eutil.GetClientOrDie()

	clusterOpts := globalOpts.DefaultClusterOptions()
	clusterOpts.ControlPlaneAvailabilityPolicy = "SingleReplica"

	hostedCluster := e2eutil.CreateNoneCluster(t, ctx, client, clusterOpts, globalOpts.ArtifactDir)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	// Since the None platform has no workers, CVO will not have expectations set,
	// which in turn means that the ClusterVersion object will never be populated.
	// Therefore only test if the control plane comes up (etc, apiserver, ...)
	e2eutil.WaitForConditionsOnHostedControlPlane(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	// etcd restarts for me once always and apiserver two times before running stable
	//e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}
