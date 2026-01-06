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
	// Start with Linux ImageType and 1 replica so the framework can validate conditions
	// We'll switch to Windows ImageType in the Run() method after scaling to 0
	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.AWS.InstanceType = "m5.metal"
	nodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeLinux

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	// Prep: Scale Linux NodePool to 0 and switch to Windows
	// This avoids Windows node readiness issues while allowing framework validation to pass
	t.Log("Prep: Scaling Linux NodePool to 0 replicas before switching to Windows")
	var currentNP hyperv1.NodePool
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &currentNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	g.Expect(currentNP.Spec.Platform.AWS.ImageType).To(Equal(hyperv1.ImageTypeLinux), "NodePool should start with Linux ImageType")

	currentNP.Spec.Replicas = &zeroReplicas
	err = it.mgmtClient.Update(ctx, &currentNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to scale NodePool to 0")

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s to scale to 0 replicas", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				return np.Status.Replicas == 0, fmt.Sprintf("expected status.replicas=0, got %d", np.Status.Replicas), nil
			},
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
	)

	t.Log("Prep: Switching from Linux to Windows ImageType")
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &currentNP)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
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

	// Cleanup: Wait for NodePool to stabilize before framework's final validation
	// After switching ImageType from Linux to Windows and doing update tests,
	// we need to wait for old machines to be deleted and platform template update to complete
	t.Log("Waiting for NodePool to stabilize (machines deleted, platform template updated)")
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s to stabilize", nodePool.Namespace, nodePool.Name),
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
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolAllMachinesReadyConditionType,
				Status: v1.ConditionTrue,
			}),
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolAllNodesHealthyConditionType,
				Status: v1.ConditionTrue,
			}),
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(10*time.Minute),
	)
	t.Log("✓ NodePool stabilized successfully")
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
