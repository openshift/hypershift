//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// SpotMarketOptionsSuccessTest tests valid SpotMarketOptions configurations
type SpotMarketOptionsSuccessTest struct {
	DummyInfraSetup
	ctx           context.Context
	hostedCluster *hyperv1.HostedCluster
}

func NewSpotMarketOptionsSuccessTest(ctx context.Context, hostedCluster *hyperv1.HostedCluster) *SpotMarketOptionsSuccessTest {
	return &SpotMarketOptionsSuccessTest{
		ctx:           ctx,
		hostedCluster: hostedCluster,
	}
}

func (test *SpotMarketOptionsSuccessTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
}

func (test *SpotMarketOptionsSuccessTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      test.hostedCluster.Name + "-spot-success-test",
			Namespace: test.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas
	if nodePool.Spec.Platform.AWS == nil {
		nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
	}
	nodePool.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
		Tenancy: "default",
		SpotMarketOptions: &hyperv1.AWSSpotMarketOptions{
			MaxPrice: ptr.To("0.50"),
		},
	}
	return nodePool, nil
}

func (test *SpotMarketOptionsSuccessTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	// Verify the NodePool was created successfully with Spot configuration
	g.Expect(nodePool.Spec.Platform.AWS.Placement.SpotMarketOptions).ToNot(BeNil())
	g.Expect(nodePool.Spec.Platform.AWS.Placement.SpotMarketOptions.MaxPrice).To(Equal(ptr.To("0.50")))
	g.Expect(nodePool.Spec.Platform.AWS.Placement.Tenancy).To(Equal("default"))

	t.Logf("Successfully created NodePool with Spot instances. MaxPrice: %v, Tenancy: %v",
		*nodePool.Spec.Platform.AWS.Placement.SpotMarketOptions.MaxPrice,
		nodePool.Spec.Platform.AWS.Placement.Tenancy)

	// Verify nodes were created
	g.Expect(nodes).ToNot(BeEmpty())
	t.Logf("Successfully provisioned %d Spot instance nodes", len(nodes))
}
