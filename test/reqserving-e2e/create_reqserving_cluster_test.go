//go:build reqserving
// +build reqserving

package reqservinge2e

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/util/reqserving"
	"k8s.io/client-go/util/retry"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCreateRequestServingIsolationCluster(t *testing.T) {
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.RawCreateOptions.RedactBaseDomain = true
	clusterOpts.Annotations = append(clusterOpts.Annotations, "hypershift.openshift.io/topology=dedicated-request-serving-components")
	z := globalOpts.ConfigurableClusterOptions.Zone.String()
	if z != "" {
		clusterOpts.AWSPlatform.Zones = strings.Split(z, ",")
	}
	if clusterOpts.NodeSelector == nil {
		clusterOpts.NodeSelector = make(map[string]string)
	}
	clusterOpts.NodeSelector[reqserving.ControlPlaneNodeLabel] = "true"

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {

		verifyRequestServing(ctx, t, g, hostedCluster)

		// Increase the number of nodes in the cluster to 3
		t.Logf("Increasing the number of nodes in the cluster to 3")
		nodePoolList := &hyperv1.NodePoolList{}
		err := mgtClient.List(ctx, nodePoolList, &crclient.ListOptions{Namespace: hostedCluster.Namespace})
		g.Expect(err).NotTo(HaveOccurred(), "List nodePools failed")
		var nodePool *hyperv1.NodePool
		for i, np := range nodePoolList.Items {
			if np.Spec.ClusterName != hostedCluster.Name {
				continue
			}
			nodePool = &nodePoolList.Items[i]
			break
		}
		g.Expect(nodePool).NotTo(BeNil(), "NodePool not found")
		replicas := int32(3)
		// Use Patch with retry to avoid update conflicts
		base := nodePool.DeepCopy()
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			nodePool.Spec.Replicas = &replicas
			return mgtClient.Patch(ctx, nodePool, crclient.MergeFrom(base))
		})
		g.Expect(err).NotTo(HaveOccurred(), "Patch nodePool replicas failed")

		// Wait for the node pool to report the new replicas
		t.Logf("Waiting for the node pool to report the new replicas")
		hcClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		e2eutil.WaitForReadyNodesByNodePool(t, ctx, hcClient, nodePool, hostedCluster.Spec.Platform.Type)

		// Wait for the hosted cluster to get the new size label
		t.Logf("Waiting for the hosted cluster to get the new size label")
		reqserving.WaitForHostedClusterSizeLabel(ctx, g, mgtClient, hostedCluster, "medium")

		// Verify that request serving pods/nodes are in the expected state after the resize
		t.Logf("Verifying request serving pods/nodes are in the expected state after the resize")
		verifyRequestServing(ctx, t, g, hostedCluster)

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "create-cluster", globalOpts.ServiceAccountSigningKey)
}

func verifyRequestServing(ctx context.Context, t *testing.T, g Gomega, hostedCluster *hyperv1.HostedCluster) {
	t.Logf("Verifying request serving control plane effects")
	err := reqserving.VerifyRequestServingCPEffects(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "VerifyRequestServingCPEffects failed")

	t.Logf("Waiting for control plane workloads to be ready")
	err = reqserving.WaitForControlPlaneWorkloadsReady(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "WaitForControlPlaneWorkloadsReady failed")

	t.Logf("Verifying request serving node allocation")
	err = reqserving.VerifyRequestServingNodeAllocation(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "VerifyRequestServingNodeAllocation failed")

	t.Logf("Verifying request serving pod distribution")
	err = reqserving.VerifyRequestServingPodDistribution(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "VerifyRequestServingPodDistribution failed")

	t.Logf("Verifying request serving placeholder config maps")
	err = reqserving.VerifyRequestServingPlaceholderConfigMaps(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "VerifyRequestServingPlaceholderConfigMaps failed")

	t.Logf("Verifying request serving pod labels")
	err = reqserving.VerifyRequestServingPodLabels(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "VerifyRequestServingPodLabels failed")
}
