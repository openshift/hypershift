//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type NodePoolImageTypeTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         e2eutil.PlatformAgnosticOptions
}

func NewNodePoolImageTypeTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) *NodePoolImageTypeTest {
	return &NodePoolImageTypeTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		clusterOpts:         clusterOpts,
		mgmtClient:          mgmtClient,
	}
}

func (it *NodePoolImageTypeTest) Setup(t *testing.T) {
	// Skip test for non-AWS platforms since ImageType is currently AWS-specific
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test is only supported for AWS platform")
	}
	if e2eutil.IsLessThan(e2eutil.Version419) {
		t.Skip("test only supported from version 4.19")
	}
	t.Log("Starting test NodePoolImageTypeTest")
}

func (it *NodePoolImageTypeTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      it.hostedCluster.Name + "-" + "test-imagetype",
			Namespace: it.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.AWS.InstanceType = "m5.metal"
	nodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	// wait for the nodepool status conditions to populate
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s to have a populated PlatformImage status condition", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type: hyperv1.NodePoolValidPlatformImageType,
			}),
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
	)

	// Check that the ValidPlatformImageType condition exists
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	if validImageCondition == nil {
		t.Fatalf("ValidPlatformImageType condition not found")
	}

	// For Windows, the condition should be True if Windows AMI mapping exists
	switch validImageCondition.Status {
	case corev1.ConditionTrue:
		// Verify message contains Windows AMI information
		if !strings.Contains(strings.ToLower(validImageCondition.Message), "windows") {
			t.Fatalf("Windows ImageType should show Windows AMI info in condition message, but got: %s", validImageCondition.Message)
		}
		t.Log("Windows ImageType test passed - Windows AMI found and validated")
	case corev1.ConditionFalse:
		// If Windows AMI mapping doesn't exist for this region/version, that's also a valid test result
		if strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Log("Windows ImageType test passed - Windows AMI not available for this region/version (expected behavior)")
		} else {
			t.Fatalf("unexpected validation failure for Windows ImageType: %s", validImageCondition.Message)
		}
	default:
		t.Fatalf("ValidPlatformImageType condition has unexpected status %s", validImageCondition.Status)
	}
}
