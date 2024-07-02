//go:build e2e
// +build e2e

package e2e

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

type NodePoolArm64CreateTest struct {
	hostedCluster *hyperv1.HostedCluster
	DummyInfraSetup
}

func NewNodePoolArm64CreateTest(hostedCluster *hyperv1.HostedCluster) *NodePoolArm64CreateTest {
	return &NodePoolArm64CreateTest{
		hostedCluster: hostedCluster,
	}
}

func (arm64np *NodePoolArm64CreateTest) Setup(t *testing.T) {
	t.Log("Starting NodePoolArm64CreateTest.")
}

func (arm64np *NodePoolArm64CreateTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      arm64np.hostedCluster.Name + "-" + "test-multiarch-create",
			Namespace: arm64np.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Arch = "arm64"
	nodePool.Spec.Platform.AWS.InstanceType = "m6g.large"

	return nodePool, nil
}

func (arm64np *NodePoolArm64CreateTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("NodePoolArm64CreateTest only tests the creation of a nodepool with arm64 architecture.")
}
