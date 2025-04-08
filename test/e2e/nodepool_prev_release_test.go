//go:build e2e
// +build e2e

package e2e

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

type NodePoolPrevReleaseCreateTest struct {
	DummyInfraSetup
	hostedCluster *hyperv1.HostedCluster
	release       string
	clusterOpts   e2eutil.PlatformAgnosticOptions
}

func NewNodePoolPrevReleaseCreateTest(hostedCluster *hyperv1.HostedCluster, release string, clusterOpts e2eutil.PlatformAgnosticOptions) *NodePoolPrevReleaseCreateTest {
	return &NodePoolPrevReleaseCreateTest{
		hostedCluster: hostedCluster,
		release:       release,
		clusterOpts:   clusterOpts,
	}
}

func (npPrevTest *NodePoolPrevReleaseCreateTest) Setup(t *testing.T) {
	t.Log("Starting NodePoolPrevReleaseCreateTest.")

	if npPrevTest.release == "" {
		t.Skip("previous release wasn't set, skipping")
	}

	isCompatible, err := e2eutil.ValidateNodePoolVersionCompatibility(npPrevTest.release, npPrevTest.hostedCluster.Spec.Release.Image)
	if err != nil || !isCompatible {
		t.Skip("nodepool release is not compatible with the current hostedCluster release, skipping")
	}
}

func (npPrevTest *NodePoolPrevReleaseCreateTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      npPrevTest.hostedCluster.Name + "-" + utilrand.String(5),
			Namespace: npPrevTest.hostedCluster.Namespace,
		},
	}

	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Release.Image = npPrevTest.release
	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (npPrevTest *NodePoolPrevReleaseCreateTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("NodePoolPrevReleaseCreateTest tests the creation of a NodePool with previous OCP release.")

	t.Logf("Validating all Nodes have the synced labels and taints")
	e2eutil.EnsureNodesLabelsAndTaints(t, nodePool, nodes)
}
