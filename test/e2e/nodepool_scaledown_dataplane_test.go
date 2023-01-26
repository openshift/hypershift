//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testNodepoolScaleDownDataPlane(parentCtx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer func() {
			t.Log("Test: NodePool Scaledown Dataplane finished")
			cancel()
		}()

		// List NodePools (should exists only one)
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: hostedCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(BeEmpty())
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostedCluster.Name + "-" + "test-scaledowndataplane",
				Namespace: hostedCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				NodeDrainTimeout: &metav1.Duration{
					Duration: 1 * time.Second,
				},
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeReplace,
					AutoRepair:  true,
				},
				ClusterName: hostedCluster.Name,
				Replicas:    &oneReplicas,
				Release: hyperv1.Release{
					Image: hostedCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: hostedCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}

		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Autorepair function: %v", nodePool.Name, err)
			}
			err = nodePoolRecreate(t, ctx, nodePool, mgmtClient)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}
		defer nodePoolScaleDownToZero(ctx, mgmtClient, *nodePool, t)

		numNodes := int32(1)

		t.Logf("Waiting for Nodes %d\n", numNodes)
		_ = e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hostedClusterClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

		// Wait for the rollout to be reported complete
		t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

		// Update NodePool images to the latest.
		t.Logf("Scalling down NodePool %s to replicas %d", nodePool.Name, zeroReplicas)
		np := nodePool.DeepCopy()
		nodePool.Spec.Replicas = &zeroReplicas
		err = mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np))
		g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool replicas")

		// Wait for NodePools to get updated
		err = wait.PollUntil(10*time.Second, func() (done bool, err error) {
			t.Logf("Waiting until NodePool scales to the desired state: Nodepool %s Replicas %d\n", nodePool.Name, zeroReplicas)
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
			g.Expect(err).NotTo(HaveOccurred())

			return nodePool.Status.Replicas == *nodePool.Spec.Replicas, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")
		t.Logf("Scale down Done!: Nodepool %s Replicas %d\n", nodePool.Name, zeroReplicas)
	}
}
