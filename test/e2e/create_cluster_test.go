//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCreateCluster implements a test that creates a cluster with the code under test
// vs upgrading to the code under test as TestUpgradeControlPlane does.
func TestCreateCluster(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// TODO (alberto): move into WaitForNReadyNodes after this PR github.com/openshift/hypershift/pull/1702 gets merged so it's validated by any call to the function in any test.
	// It's has to wait for the PR to merged otherwise the control_plane_upgrade_test would fail.
	t.Logf("Validating all Nodes have the NodePool label")
	nodes := &corev1.NodeList{}
	if err := guestClient.List(ctx, nodes); err != nil {
		t.Fatalf("failed to list nodes in guest cluster: %v", err)
	}
	for _, node := range nodes.Items {
		g.Expect(node.Labels[hyperv1.NodePoolLabel]).NotTo(BeEmpty())
	}

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, guestClient, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	e2eutil.EnsureNodeCommunication(t, ctx, client, hostedCluster)
}

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = "SingleReplica"

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	// Since the None platform has no workers, CVO will not have expectations set,
	// which in turn means that the ClusterVersion object will never be populated.
	// Therefore only test if the control plane comes up (etc, apiserver, ...)
	e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, client, hostedCluster, globalOpts.LatestReleaseImage)

	// etcd restarts for me once always and apiserver two times before running stable
	// e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}

// TestCreateClusterPrivate implements a smoke test that creates a private cluster.
// Validations requiring guest cluster client are dropped here since the kas is not accessible when private.
// In the future we might want to leverage https://issues.redhat.com/browse/HOSTEDCP-697 to access guest cluster.
func TestCreateClusterPrivate(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Private)

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	_, err = e2eutil.WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

	// Ensure NodePools have all Nodes ready.
	waitTimeout := 30 * time.Minute
	err = wait.PollImmediateWithContext(ctx, 5*time.Second, waitTimeout, func(ctx context.Context) (done bool, err error) {
		var nodePoolList hyperv1.NodePoolList
		if err := client.List(ctx, &nodePoolList, &crclient.ListOptions{Namespace: hostedCluster.Namespace}); err != nil {
			t.Fatalf("failed to list nodepools: %v", err)
		}
		for _, nodePool := range nodePoolList.Items {
			if *nodePool.Spec.Replicas != nodePool.Status.Replicas {
				t.Logf("Waiting. NodePool %q wants %v replicas but has %v", nodePool.Name, *nodePool.Spec.Replicas, nodePool.Status.Replicas)
				return false, nil
			}
		}
		return true, nil
	})

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRolloutWithNoPreRolloutPlatformCheck(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}
