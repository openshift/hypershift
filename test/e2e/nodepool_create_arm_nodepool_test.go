//go:build e2e
// +build e2e

package e2e

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

type CreateArmNodePoolTest struct {
	hostedCluster *hyperv1.HostedCluster
	clusterOpts   core.CreateOptions
}

func NewCreateClusterWithArmNodePoolTest(hostedCluster *hyperv1.HostedCluster, clusterOpts core.CreateOptions) *CreateArmNodePoolTest {
	return &CreateArmNodePoolTest{
		hostedCluster: hostedCluster,
		clusterOpts:   clusterOpts,
	}
}

func (a *CreateArmNodePoolTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}

	t.Log("Starting test CreateArmNodePoolTest")
}

func (a *CreateArmNodePoolTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      a.hostedCluster.Name + "-" + "test-arm-nodepool",
			Namespace: a.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Arch = "arm64"
	nodePool.Spec.Platform.AWS.InstanceType = "m6g.large"

	return nodePool, nil
}

func (a *CreateArmNodePoolTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	providerID := nodes[0].Spec.ProviderID
	g.Expect(providerID).NotTo(BeEmpty())

	instanceID := providerID[strings.LastIndex(providerID, "/")+1:]
	t.Logf("instanceID: %s", instanceID)
}
