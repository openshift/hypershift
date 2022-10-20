//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	//corev1 "k8s.io/api/core/v1"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "github.com/onsi/gomega"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ns              = "jparrill"
	hcName          = "jparrill-dev"
	annotationDrain = "machine.cluster.x-k8s.io/exclude-node-draining"
)

func TestNodePoolMain(t *testing.T) {
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(context.TODO())
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	defer cancel()

	mgmtClient, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	guestClusters := &hyperv1.HostedClusterList{}
	err = mgmtClient.List(ctx, guestClusters, &crclient.ListOptions{
		Namespace: ns,
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s already created Cluster")

	guestClient := e2eutil.WaitForGuestClient(t, ctx, mgmtClient, &guestClusters.Items[0])
	guestCluster := &guestClusters.Items[0]

	fmt.Println(guestCluster)
	g.Expect(guestCluster.Name).To(Equal(hcName))

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, mgmtClient, guestClient, guestCluster, globalOpts.LatestReleaseImage)

	t.Run("TestSetAutoRepair", testSetAutoRepair(ctx, mgmtClient, guestCluster, guestClient, clusterOpts))
	//t.Run("KillAllMembers", testKillAllMembers(ctx, client, cluster))
}
