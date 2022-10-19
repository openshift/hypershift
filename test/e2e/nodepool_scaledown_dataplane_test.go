//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestScaleDownDataPlane(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	var nodePools hyperv1.NodePoolList
	var zeroReplicas int32 = 0

	testContext := context.Background()
	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	numZones := int32(len(clusterOpts.AWSPlatform.Zones))
	if numZones <= 1 {
		clusterOpts.NodePoolReplicas = 3
	} else if numZones == 2 {
		clusterOpts.NodePoolReplicas = 2
	} else {
		clusterOpts.NodePoolReplicas = 1
	}
	clusterOpts.AutoRepair = true
	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch v := o.(type) {
		case *hyperv1.NodePool:
			v.Spec.NodeDrainTimeout = &metav1.Duration{
				Duration: 1 * time.Second,
			}
		}
	}

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for HC to finish the deployment
	t.Logf("Waiting for initial cluster rollout. Image: %s", hostedCluster.Spec.Release.Image)
	e2eutil.WaitForImageRollout(t, ctx, client, guestClient, hostedCluster, hostedCluster.Spec.Release.Image)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Find NodePools.
	err = client.List(ctx, &nodePools, &crclient.ListOptions{Namespace: hostedCluster.Namespace})
	g.Expect(err).NotTo(HaveOccurred(), "failed to list NodePools")
	numNodes := int32(numZones * clusterOpts.NodePoolReplicas)

	// Wait for NodePools to roll out the initial version.
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Update NodePool images to the latest.
	for _, nodePool := range nodePools.Items {
		err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

		t.Logf("Scalling down NodePool %s to replicas %d", nodePool.Name, zeroReplicas)
		original := nodePool.DeepCopy()
		nodePool.Spec.Replicas = &zeroReplicas
		err = client.Patch(ctx, &nodePool, crclient.MergeFrom(original))
		g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool replicas")
	}

	// Wait for NodePools to get updated
	for _, nodePool := range nodePools.Items {
		err := wait.PollUntil(10*time.Second, func() (done bool, err error) {
			t.Logf("Waiting until NodePool scales to the desired state: Nodepool %s Replicas %d\n", nodePool.Name, zeroReplicas)
			err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

			return nodePool.Status.Replicas == *nodePool.Spec.Replicas, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")
		t.Logf("Scale down Done!: Nodepool %s Replicas %d\n", nodePool.Name, zeroReplicas)
	}
}
