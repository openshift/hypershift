//go:build e2e

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
	// Start with Linux ImageType and 0 replicas to skip framework's node readiness check
	// We'll switch to Windows ImageType in the Run() method for testing
	nodePool.Spec.Replicas = &zeroReplicas
	nodePool.Spec.Platform.AWS.InstanceType = "m5.metal"
	nodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeLinux

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) ExpectedNodeCount() int {
	// Skip framework's node readiness check since we're testing ImageType functionality
	// at 0 replicas (avoiding both Linux and Windows node readiness issues in CI)
	return 0
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	// Prep: Switch from Linux to Windows ImageType
	// NodePool already starts at 0 replicas to avoid node readiness issues
	t.Log("Prep: Switching from Linux to Windows ImageType")
	var currentNP hyperv1.NodePool
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &currentNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	g.Expect(currentNP.Spec.Platform.AWS.ImageType).To(Equal(hyperv1.ImageTypeLinux), "NodePool should start with Linux ImageType")
	g.Expect(*currentNP.Spec.Replicas).To(Equal(int32(0)), "NodePool should start with 0 replicas")

	currentNP.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
	err = it.mgmtClient.Update(ctx, &currentNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to switch to Windows ImageType")

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s to switch to Windows ImageType", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := hyperv1.ImageTypeWindows, np.Spec.Platform.AWS.ImageType
				return want == got, fmt.Sprintf("expected imageType %s, got %s", want, got), nil
			},
		},
		e2eutil.WithInterval(2*time.Second), e2eutil.WithTimeout(1*time.Minute),
	)

	// Get the updated nodePool for the tests
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	t.Log("✓ Prep complete: NodePool is now at 0 replicas with Windows ImageType")

	t.Log("Test 1: Verifying Windows ImageType creation")
	it.testWindowsImageTypeCreation(t, g, ctx, nodePool)

	t.Log("Test 2: Testing ImageType updates (Windows → Linux → Windows)")
	it.testImageTypeUpdate(t, g, ctx, nodePool)

	t.Log("All NodePool ImageType tests passed successfully")

	// Wait for platform template update to complete before framework's final validation
	// Note: We don't wait for AllMachinesReady or AllNodesHealthy since those are
	// always False for 0-replica NodePools, and our fix to ExpectedNodePoolConditions()
	// makes the framework correctly expect False for those conditions.
	t.Log("Waiting for platform template update to complete")
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s platform template update to complete", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
				Status: v1.ConditionFalse,
			}),
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
	)
	t.Log("✓ Platform template update completed successfully")
}

func (it *NodePoolImageTypeTest) testWindowsImageTypeCreation(t *testing.T, g *WithT, ctx context.Context, nodePool hyperv1.NodePool) {
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

	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	if validImageCondition == nil {
		t.Fatalf("ValidPlatformImageType condition not found")
	}

	switch validImageCondition.Status {
	case corev1.ConditionTrue:
		// Condition True means Windows AMI was found successfully
		// Verify the message contains an AMI ID (format: ami-xxxxx)
		if !strings.Contains(strings.ToLower(validImageCondition.Message), "ami-") {
			t.Fatalf("Windows ImageType condition True but message doesn't contain AMI ID: %s", validImageCondition.Message)
		}
		t.Logf("✓ Windows ImageType creation test passed - Windows AMI found and validated: %s", validImageCondition.Message)
	case corev1.ConditionFalse:
		if strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Log("✓ Windows ImageType creation test passed - Windows AMI not available for this region/version (expected behavior)")
		} else {
			t.Fatalf("unexpected validation failure for Windows ImageType: %s", validImageCondition.Message)
		}
	default:
		t.Fatalf("ValidPlatformImageType condition has unexpected status %s", validImageCondition.Status)
	}
}

func (it *NodePoolImageTypeTest) testImageTypeUpdate(t *testing.T, g *WithT, ctx context.Context, nodePool hyperv1.NodePool) {
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	g.Expect(nodePool.Spec.Platform.AWS.ImageType).To(Equal(hyperv1.ImageTypeWindows), "NodePool should start with Windows ImageType")
	t.Logf("✓ Checkpoint: NodePool has Windows ImageType: %s", nodePool.Spec.Platform.AWS.ImageType)

	t.Log("Updating ImageType from Windows to Linux")
	nodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeLinux
	err = it.mgmtClient.Update(ctx, &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool to Linux ImageType")

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s imageType to update to Linux", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := hyperv1.ImageTypeLinux, np.Spec.Platform.AWS.ImageType
				return want == got, fmt.Sprintf("expected imageType %s, got %s", want, got), nil
			},
		},
		e2eutil.WithInterval(2*time.Second), e2eutil.WithTimeout(1*time.Minute),
	)
	t.Logf("✓ Checkpoint: ImageType successfully updated to Linux")

	t.Log("Reverting ImageType from Linux back to Windows")
	var updatedNP hyperv1.NodePool
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &updatedNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	updatedNP.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
	err = it.mgmtClient.Update(ctx, &updatedNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool back to Windows ImageType")

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s imageType to revert to Windows", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := hyperv1.ImageTypeWindows, np.Spec.Platform.AWS.ImageType
				return want == got, fmt.Sprintf("expected imageType %s, got %s", want, got), nil
			},
		},
		e2eutil.WithInterval(2*time.Second), e2eutil.WithTimeout(1*time.Minute),
	)
	t.Logf("✓ Checkpoint: ImageType successfully reverted to Windows")

	t.Log("✓ ImageType update test passed - bidirectional updates work correctly")
}
