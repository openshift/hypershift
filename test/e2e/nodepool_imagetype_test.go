//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
	// Create a new NodePool with Windows ImageType for scaling tests
	nodePool := defaultNodepool.DeepCopy()
	nodePool.ObjectMeta.Name = it.hostedCluster.Name + "-test-imagetype"

	// Clear fields that should not be set on creation
	nodePool.ObjectMeta.ResourceVersion = ""
	nodePool.ObjectMeta.UID = ""
	nodePool.ObjectMeta.CreationTimestamp = metav1.Time{}
	nodePool.ObjectMeta.Generation = 0

	// Start with 1 replica and Windows ImageType
	replicas := int32(1)
	nodePool.Spec.Replicas = &replicas
	nodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	t.Log("Test: NodePool ImageType persistence through scaling operations")

	// NodePool was created with Windows ImageType and 1 replica
	t.Logf("NodePool created: %s/%s with ImageType=%s, Replicas=1",
		nodePool.Namespace, nodePool.Name, nodePool.Spec.Platform.AWS.ImageType)

	// Test scaling operations (starting from 1 replica)
	it.testImageTypePersistenceThroughScaling(t, g, ctx, &nodePool)

	t.Log("All NodePool ImageType scaling tests passed successfully")
}

func (it *NodePoolImageTypeTest) testImageTypePersistenceThroughScaling(t *testing.T, g *WithT, ctx context.Context, nodePool *hyperv1.NodePool) {
	t.Log("Scaling NodePool to 0 replicas")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 0, hyperv1.ImageTypeWindows)

	t.Log("Scaling NodePool to 2 replicas")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 2, hyperv1.ImageTypeWindows)

	t.Log("Scaling NodePool to 1 replica")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 1, hyperv1.ImageTypeWindows)

	t.Log("Scaling back to 0 replicas to verify persistence")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 0, hyperv1.ImageTypeWindows)

	// Scale back to 1 replica for final validation
	// This ensures the test framework's final status check passes (it expects healthy nodes)
	t.Log("Scaling back to 1 replica for final validation")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 1, hyperv1.ImageTypeWindows)
}

func (it *NodePoolImageTypeTest) scaleAndVerifyImageType(t *testing.T, g *WithT, ctx context.Context, nodePool *hyperv1.NodePool, targetReplicas int32, expectedImageType hyperv1.ImageType) {
	// Get current NodePool state
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

	// Track previous replica count for event validation
	previousReplicas := nodePool.Status.Replicas

	// Update replicas
	nodePool.Spec.Replicas = &targetReplicas
	err = it.mgmtClient.Update(ctx, nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to scale NodePool to %d replicas", targetReplicas)

	// Wait for both spec and status replicas to match, and verify ImageType persists
	timeout := 5 * time.Minute
	if targetReplicas > 0 {
		// Windows nodes take longer to provision (18+ minutes based on test results)
		timeout = 30 * time.Minute
	}

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool to scale to %d replicas", targetReplicas),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				specReplicas := int32(0)
				if np.Spec.Replicas != nil {
					specReplicas = *np.Spec.Replicas
				}
				statusReplicas := np.Status.Replicas
				imageType := np.Spec.Platform.AWS.ImageType

				replicasMatch := specReplicas == targetReplicas && statusReplicas == targetReplicas
				imageTypeMatch := imageType == expectedImageType

				if !replicasMatch || !imageTypeMatch {
					return false, fmt.Sprintf("expected spec.replicas=%d status.replicas=%d imageType=%s, got spec.replicas=%d status.replicas=%d imageType=%s",
						targetReplicas, targetReplicas, expectedImageType, specReplicas, statusReplicas, imageType), nil
				}
				return true, "", nil
			},
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(timeout),
	)

	// If scaling up, wait for nodes to be ready
	if targetReplicas > 0 {
		t.Logf("Waiting for %d nodes to become ready", targetReplicas)
		e2eutil.WaitForReadyNodesByNodePool(t, ctx, it.hostedClusterClient, nodePool, hyperv1.AWSPlatform)
		t.Logf("All %d nodes are ready", targetReplicas)
	}

	// Verify ImageType is still correct after scaling
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool after scaling")
	g.Expect(nodePool.Spec.Platform.AWS.ImageType).To(Equal(expectedImageType),
		"ImageType should persist through scaling operations")

	t.Logf("Scaled to %d replicas, ImageType persisted: %s", targetReplicas, expectedImageType)

	// Check for scaling events to confirm the operation was processed
	it.checkScalingEvents(t, ctx, nodePool, previousReplicas, targetReplicas)
}

// checkScalingEvents verifies that NodePool events related to scaling were generated
func (it *NodePoolImageTypeTest) checkScalingEvents(t *testing.T, ctx context.Context, nodePool *hyperv1.NodePool, previousReplicas, currentReplicas int32) {
	// List events for the NodePool
	events := &corev1.EventList{}
	err := it.mgmtClient.List(ctx, events, crclient.InNamespace(nodePool.Namespace))
	if err != nil {
		t.Logf("Warning: Could not list events: %v", err)
		return
	}

	// Filter events related to this NodePool
	var nodePoolEvents []corev1.Event
	for _, event := range events.Items {
		// Check if event is related to this NodePool or its child resources
		if event.InvolvedObject.Name == nodePool.Name ||
			strings.Contains(event.Message, nodePool.Name) ||
			(event.InvolvedObject.Namespace == nodePool.Namespace &&
				strings.Contains(event.InvolvedObject.Name, nodePool.Name)) {
			nodePoolEvents = append(nodePoolEvents, event)
		}
	}

	if len(nodePoolEvents) > 0 {
		t.Logf("Found %d events related to NodePool %s", len(nodePoolEvents), nodePool.Name)

		// Log recent scaling-related events for debugging
		scalingEventCount := 0
		for _, event := range nodePoolEvents {
			// Look for events that indicate scaling activity
			msg := strings.ToLower(event.Message)
			reason := event.Reason

			if strings.Contains(msg, "scale") ||
				strings.Contains(msg, "replica") ||
				strings.Contains(msg, "machine") ||
				strings.Contains(reason, "Scale") ||
				strings.Contains(reason, "Replica") {
				scalingEventCount++
				t.Logf("  Scaling event: %s - %s: %s", event.Type, event.Reason, event.Message)
			}
		}

		if scalingEventCount > 0 {
			t.Logf("✓ Found %d scaling-related events confirming scaling operation", scalingEventCount)
		} else {
			t.Logf("Note: No explicit scaling events found, but status.replicas updated from %d to %d", previousReplicas, currentReplicas)
		}
	} else {
		t.Logf("Note: No events found for NodePool, but scaling verified via status.replicas (%d → %d)", previousReplicas, currentReplicas)
	}
}
