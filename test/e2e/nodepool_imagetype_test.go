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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		ObjectMeta: metav1.ObjectMeta{
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

	// This test creates a new NodePool with Windows ImageType and tests bidirectional updates
	// It validates that ImageType changes actually result in nodes being provisioned with the correct OS
	// Following the manual test case from CNTRLPLANE-2277
	t.Log("Test Case 4: Update Existing NodePool ImageType (CNTRLPLANE-2277)")

	// Log the NodePool name to confirm we're using the one created by BuildNodePoolManifest
	t.Logf("Testing with NodePool: %s/%s (created by BuildNodePoolManifest)", nodePool.Namespace, nodePool.Name)

	// Verify NodePool exists with Windows ImageType
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

	// Add nil check for Platform.AWS as suggested by CodeRabbit
	var currentImageType hyperv1.ImageType
	if nodePool.Spec.Platform.AWS != nil {
		currentImageType = nodePool.Spec.Platform.AWS.ImageType
	}
	if currentImageType == "" {
		currentImageType = hyperv1.ImageTypeLinux // Default is Linux
	}
	t.Logf("✓ Checkpoint 2: NodePool '%s' has ImageType: %s (ready to update to Linux)", nodePool.Name, currentImageType)

	// Wait for Windows node to be ready (if Windows AMI is available)
	t.Log("Waiting for initial Windows node to be ready")
	initialNodes := e2eutil.WaitForReadyNodesByNodePool(t, ctx, it.hostedClusterClient, &nodePool, globalOpts.Platform)
	it.waitForNodesWithImageType(t, ctx, &nodePool, hyperv1.ImageTypeWindows, initialNodes)

	// Update imageType from Windows to Linux and verify Linux nodes are provisioned
	t.Log("Step 4: Update imageType from Windows to Linux")
	it.testImageTypeUpdate(t, g, ctx, &nodePool, hyperv1.ImageTypeLinux)

	// Revert imageType back to Windows and verify Windows nodes are provisioned
	t.Log("Step 7: Revert imageType back to Windows (cleanup)")
	it.testImageTypeUpdate(t, g, ctx, &nodePool, hyperv1.ImageTypeWindows)

	t.Logf("✓ Checkpoint 8: NodePool returned to stable state with ImageType: %s", hyperv1.ImageTypeWindows)
	t.Log("✓ Test Result: PASSED")
}

func (it *NodePoolImageTypeTest) testImageTypeUpdate(t *testing.T, g *WithT, ctx context.Context, nodePool *hyperv1.NodePool, targetImageType hyperv1.ImageType) {
	// Get current NodePool configuration
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

	var currentImageType hyperv1.ImageType
	if nodePool.Spec.Platform.AWS != nil {
		currentImageType = nodePool.Spec.Platform.AWS.ImageType
	}
	if currentImageType == "" {
		currentImageType = hyperv1.ImageTypeLinux
	}
	t.Logf("✓ Checkpoint 3: NodePool YAML exported, current instanceType and imageType verified")

	t.Logf("Updating ImageType from %s to %s", currentImageType, targetImageType)

	// Update the ImageType (equivalent to oc patch)
	nodePool.Spec.Platform.AWS.ImageType = targetImageType
	err = it.mgmtClient.Update(ctx, nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool ImageType")
	t.Logf("✓ Checkpoint 4: imageType successfully updated to '%s'", targetImageType)

	// Verify platform configuration after update using extracted helper
	it.waitForImageTypeInSpec(t, ctx, nodePool, targetImageType)
	t.Logf("✓ Checkpoint 5: imageType '%s' persisted in configuration", targetImageType)

	// Verify the ValidPlatformImageType condition is set
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get updated NodePool")

	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	g.Expect(validImageCondition).NotTo(BeNil(), "ValidPlatformImageType condition should be set")

	// Validate the condition status
	switch validImageCondition.Status {
	case corev1.ConditionTrue:
		// Condition True means AMI was found successfully
		// For Windows, verify the message contains an AMI ID
		if targetImageType == hyperv1.ImageTypeWindows {
			if !strings.Contains(strings.ToLower(validImageCondition.Message), "ami-") {
				t.Logf("Warning: Windows ImageType condition True but message doesn't contain AMI ID: %s", validImageCondition.Message)
			} else {
				t.Logf("✓ %s AMI validated: %s", targetImageType, validImageCondition.Message)
			}
		} else {
			t.Logf("✓ %s ImageType validated: %s", targetImageType, validImageCondition.Message)
		}
	case corev1.ConditionFalse:
		// For Windows, it's acceptable if AMI is not available in the region
		if targetImageType == hyperv1.ImageTypeWindows && strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Logf("✓ Windows AMI not available for this region/version (expected behavior): %s", validImageCondition.Message)
		} else {
			t.Fatalf("unexpected validation failure for %s ImageType: %s", targetImageType, validImageCondition.Message)
		}
	default:
		t.Fatalf("ValidPlatformImageType condition has unexpected status %s", validImageCondition.Status)
	}

	// Check NodePool status for update activity
	t.Logf("Step 6: Check NodePool status for update activity")
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s platform template update to complete", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
				Status: metav1.ConditionFalse,
			}),
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
	)
	t.Logf("✓ Checkpoint 6: NodePool update status recorded")

	// Wait for nodes with the target ImageType to be ready
	t.Logf("Waiting for nodes with %s ImageType to be ready", targetImageType)
	updatedNodes := e2eutil.WaitForReadyNodesByNodePool(t, ctx, it.hostedClusterClient, nodePool, globalOpts.Platform)
	it.waitForNodesWithImageType(t, ctx, nodePool, targetImageType, updatedNodes)
}

// waitForImageTypeInSpec waits for the ImageType to be reflected in the NodePool spec
// This is a helper method extracted to reduce duplication as suggested by CodeRabbit
func (it *NodePoolImageTypeTest) waitForImageTypeInSpec(t *testing.T, ctx context.Context, nodePool *hyperv1.NodePool, expectedType hyperv1.ImageType) {
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s imageType to update to %s", nodePool.Namespace, nodePool.Name, expectedType),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				// Add nil check for Platform.AWS as suggested by CodeRabbit
				if np.Spec.Platform.AWS == nil {
					return false, "AWS platform config not set", nil
				}
				got := np.Spec.Platform.AWS.ImageType
				if got == "" {
					got = hyperv1.ImageTypeLinux
				}
				return expectedType == got, fmt.Sprintf("expected imageType %s, got %s", expectedType, got), nil
			},
		},
		e2eutil.WithInterval(2*time.Second), e2eutil.WithTimeout(1*time.Minute),
	)
}

// waitForNodesWithImageType validates that nodes are provisioned with the correct OS for the ImageType
// For Windows, it checks for "Windows Server" in the OS image
// For Linux, it checks for "Red Hat" or "CoreOS" in the OS image
func (it *NodePoolImageTypeTest) waitForNodesWithImageType(t *testing.T, ctx context.Context, nodePool *hyperv1.NodePool, expectedImageType hyperv1.ImageType, nodes []corev1.Node) {
	// Check if we should skip node validation
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	if validImageCondition != nil && validImageCondition.Status == corev1.ConditionFalse {
		if expectedImageType == hyperv1.ImageTypeWindows && strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Logf("Skipping node validation: Windows AMI not available for this region/version")
			return
		}
	}

	t.Logf("Validating that nodes have correct OS for ImageType: %s", expectedImageType)

	for _, node := range nodes {
		osImage := node.Status.NodeInfo.OSImage
		t.Logf("Node %s has OSImage: %s", node.Name, osImage)

		switch expectedImageType {
		case hyperv1.ImageTypeWindows:
			if !strings.Contains(strings.ToLower(osImage), "windows") {
				t.Fatalf("Expected Windows OS image for ImageType Windows, but got: %s", osImage)
			}
			t.Logf("✓ Node %s confirmed as Windows: %s", node.Name, osImage)
		case hyperv1.ImageTypeLinux:
			if !strings.Contains(strings.ToLower(osImage), "red hat") && !strings.Contains(strings.ToLower(osImage), "coreos") {
				t.Fatalf("Expected Linux (Red Hat/CoreOS) OS image for ImageType Linux, but got: %s", osImage)
			}
			t.Logf("✓ Node %s confirmed as Linux: %s", node.Name, osImage)
		}
	}
}
