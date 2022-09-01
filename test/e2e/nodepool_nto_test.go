//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	customTuned = `apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: openshift-dummy
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Custom OpenShift profile
      include=openshift-node
      [sysctl]
      vm.dirty_ratio="55"
    name: openshift-dummy
  recommend:
  - match:
    - label: profile
    priority: 20
    profile: openshift-dummy
`

	hypershiftNodePoolNameLabel = "hypershift.openshift.io/nodePoolName" // HyperShift-enabled NTO adds this label to Tuned CRs bound to NodePools
	operatorNamespaceDefault    = "openshift-cluster-node-tuning-operator"
	tunedConfigKey              = "tuned"
)

func TestNodepoolTunedConfigGetsRolledout(t *testing.T) {
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
	t.Logf("waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be complete
	t.Logf("waiting for cluster rollout, release image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, guestClient, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	tunedConfigConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-tuned-test",
			Namespace: hostedCluster.Namespace,
		},
		Data: map[string]string{tunedConfigKey: customTuned},
	}
	if err := client.Create(ctx, tunedConfigConfigMap); err != nil {
		t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
	}

	nodePools := &hyperv1.NodePoolList{}
	if err := client.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	}

	var nodePool hyperv1.NodePool
	for _, nodePool = range nodePools.Items {
		if nodePool.Spec.ClusterName != hostedCluster.Name {
			continue
		}

		np := nodePool.DeepCopy()
		nodePool.Spec.TunedConfig = append(nodePool.Spec.TunedConfig, corev1.LocalObjectReference{Name: tunedConfigConfigMap.Name})
		if err := client.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
		}
	}

	hostedTunedList := &tunedv1.TunedList{}
	hostedTunedLabels := crclient.MatchingLabels{hypershiftNodePoolNameLabel: nodePool.GetName()}

	t.Logf("waiting for rollout of hosted Tuned")
	err = wait.PollImmediateWithContext(ctx, 20*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
		if ctx.Err() != nil {
			return false, err
		}

		if err := guestClient.List(ctx, hostedTunedList, crclient.InNamespace(operatorNamespaceDefault), hostedTunedLabels); err != nil {
			t.Logf("WARNING: failed to list Tuneds, will retry: %v", err)
			return false, nil
		}
		if len(hostedTunedList.Items) == 0 {
			t.Logf("no custom Tuned objects with label %s", hypershiftNodePoolNameLabel)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for hosted Tuned: %v", err)
	}
}
