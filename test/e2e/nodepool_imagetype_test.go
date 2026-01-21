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
	"k8s.io/apimachinery/pkg/labels"
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
	// Start with default Linux ImageType, we'll update to Windows in Run()
	// ImageType intentionally not set - defaults to Linux

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	t.Logf("Testing NodePool ImageType bidirectional updates: %s/%s", nodePool.Namespace, nodePool.Name)

	// Verify NodePool was created with Linux ImageType (default)
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

	var currentImageType hyperv1.ImageType
	if nodePool.Spec.Platform.AWS != nil {
		currentImageType = nodePool.Spec.Platform.AWS.ImageType
	}
	if currentImageType == "" {
		currentImageType = hyperv1.ImageTypeLinux
	}
	t.Logf("Initial ImageType: %s", currentImageType)

	// Validate initial Linux nodes are ready (provided by test framework)
	t.Logf("Validating %d initial Linux node(s) from test framework", len(nodes))
	for _, node := range nodes {
		osImage := node.Status.NodeInfo.OSImage
		if !strings.Contains(strings.ToLower(osImage), "red hat") && !strings.Contains(strings.ToLower(osImage), "coreos") {
			t.Fatalf("Expected Linux (RHCOS) node, got: %s (node: %s)", osImage, node.Name)
		}
		t.Logf("Node %s running Linux: %s", node.Name, osImage)
	}

	// Test update from Linux to Windows
	t.Log("Updating ImageType from Linux to Windows...")
	it.testImageTypeUpdate(t, g, ctx, &nodePool, hyperv1.ImageTypeWindows)

	// Test revert from Windows back to Linux
	t.Log("Reverting ImageType from Windows back to Linux...")
	it.testImageTypeUpdate(t, g, ctx, &nodePool, hyperv1.ImageTypeLinux)

	t.Log("NodePool ImageType bidirectional test completed successfully")
}

func (it *NodePoolImageTypeTest) testImageTypeUpdate(t *testing.T, g *WithT, ctx context.Context, nodePool *hyperv1.NodePool, targetImageType hyperv1.ImageType) {
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

	var currentImageType hyperv1.ImageType
	if nodePool.Spec.Platform.AWS != nil {
		currentImageType = nodePool.Spec.Platform.AWS.ImageType
	}
	if currentImageType == "" {
		currentImageType = hyperv1.ImageTypeLinux
	}

	t.Logf("Current ImageType: %s, Target ImageType: %s", currentImageType, targetImageType)

	// Update the ImageType
	nodePool.Spec.Platform.AWS.ImageType = targetImageType
	err = it.mgmtClient.Update(ctx, nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool ImageType")

	// Wait for ImageType to be reflected in spec
	it.waitForImageTypeInSpec(t, ctx, nodePool, targetImageType)
	t.Logf("ImageType updated to %s in NodePool spec", targetImageType)

	// Verify the ValidPlatformImageType condition is set
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get updated NodePool")

	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	g.Expect(validImageCondition).NotTo(BeNil(), "ValidPlatformImageType condition should be set")

	// Validate the AMI/image condition
	switch validImageCondition.Status {
	case corev1.ConditionTrue:
		if targetImageType == hyperv1.ImageTypeWindows {
			if !strings.Contains(strings.ToLower(validImageCondition.Message), "ami-") {
				t.Logf("Warning: Windows AMI condition True but no AMI ID in message: %s", validImageCondition.Message)
			} else {
				t.Logf("Windows AMI found: %s", validImageCondition.Message)
			}
		} else {
			t.Logf("Linux AMI validated: %s", validImageCondition.Message)
		}
	case corev1.ConditionFalse:
		if targetImageType == hyperv1.ImageTypeWindows && strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Logf("Windows AMI not available in this region (expected): %s", validImageCondition.Message)
		} else {
			t.Fatalf("AMI validation failed for %s: %s", targetImageType, validImageCondition.Message)
		}
	default:
		t.Fatalf("ValidPlatformImageType condition has unexpected status: %s", validImageCondition.Status)
	}

	// Wait for platform template update to complete
	t.Log("Waiting for platform template update to complete...")
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

	// Wait for nodes with the target ImageType to be ready
	// Note: ImageType changes trigger platform template updates, not config updates,
	// so we wait for nodes directly using the OS predicate without waiting for config update
	t.Logf("Waiting for %s nodes to be provisioned...", targetImageType)
	_ = it.waitForNodesWithImageType(t, ctx, nodePool, targetImageType)
	t.Logf("Successfully validated %s nodes", targetImageType)
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

// waitForNodesWithImageType waits for nodes to be ready with the correct OS for the ImageType
// For Windows, it checks for "windows" in the OS image
// For Linux, it checks for "Red Hat" or "CoreOS" in the OS image
// Returns the nodes once they are ready with the correct OS
func (it *NodePoolImageTypeTest) waitForNodesWithImageType(t *testing.T, ctx context.Context, nodePool *hyperv1.NodePool, expectedImageType hyperv1.ImageType) []corev1.Node {
	// Skip validation if Windows AMI is not available
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	if validImageCondition != nil && validImageCondition.Status == corev1.ConditionFalse {
		if expectedImageType == hyperv1.ImageTypeWindows && strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Log("Skipping node OS validation: Windows AMI not available in this region")
			// Fall back to waiting for any ready nodes
			return e2eutil.WaitForReadyNodesByNodePool(t, ctx, it.hostedClusterClient, nodePool, globalOpts.Platform)
		}
	}

	// Wait for nodes with the expected OS to be ready
	nodes := e2eutil.WaitForNReadyNodesWithOptions(t, ctx, it.hostedClusterClient, *nodePool.Spec.Replicas, globalOpts.Platform,
		fmt.Sprintf("for NodePool %s/%s with %s OS", nodePool.Namespace, nodePool.Name, expectedImageType),
		e2eutil.WithClientOptions(
			crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: nodePool.Name})},
		),
		e2eutil.WithPredicates(
			e2eutil.Predicate[*corev1.Node](func(node *corev1.Node) (done bool, reasons string, err error) {
				osImage := node.Status.NodeInfo.OSImage

				switch expectedImageType {
				case hyperv1.ImageTypeWindows:
					if !strings.Contains(strings.ToLower(osImage), "windows") {
						return false, fmt.Sprintf("Expected Windows OS, got: %s (node: %s)", osImage, node.Name), nil
					}
					return true, fmt.Sprintf("Node %s running Windows: %s", node.Name, osImage), nil
				case hyperv1.ImageTypeLinux:
					if !strings.Contains(strings.ToLower(osImage), "red hat") && !strings.Contains(strings.ToLower(osImage), "coreos") {
						return false, fmt.Sprintf("Expected Linux (RHCOS), got: %s (node: %s)", osImage, node.Name), nil
					}
					return true, fmt.Sprintf("Node %s running Linux: %s", node.Name, osImage), nil
				default:
					return false, fmt.Sprintf("Unknown ImageType: %s", expectedImageType), nil
				}
			}),
		),
	)

	// Log the successful validation
	for _, node := range nodes {
		osImage := node.Status.NodeInfo.OSImage
		t.Logf("Node %s ready with OS: %s", node.Name, osImage)
	}

	return nodes
}
