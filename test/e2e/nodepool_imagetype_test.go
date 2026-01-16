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
	// Return nil to indicate we don't want to create a new NodePool
	// We'll use the existing default NodePool for scaling tests
	return nil, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	t.Log("Test: NodePool ImageType persistence through scaling operations")

	// Get the default NodePool for this cluster
	var defaultNodePool hyperv1.NodePool
	err := it.mgmtClient.Get(ctx, crclient.ObjectKey{
		Namespace: it.hostedCluster.Namespace,
		Name:      it.hostedCluster.Name,
	}, &defaultNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get default NodePool")

	// Get original state
	originalReplicas := defaultNodePool.Spec.Replicas
	originalImageType := defaultNodePool.Spec.Platform.AWS.ImageType
	if originalImageType == "" {
		originalImageType = hyperv1.ImageTypeLinux // Default is Linux
	}
	t.Logf("✓ Initial state - Replicas: %d, ImageType: %s", *originalReplicas, originalImageType)

	// Update to Windows ImageType before scaling tests
	t.Log("Setting ImageType to Windows for scaling tests")
	defaultNodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
	err = it.mgmtClient.Update(ctx, &defaultNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool to Windows ImageType")

	// Wait for Windows ImageType to be reflected
	e2eutil.EventuallyObject(t, ctx, "wait for Windows imageType to be set",
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&defaultNodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
				return np.Spec.Platform.AWS.ImageType == hyperv1.ImageTypeWindows,
					fmt.Sprintf("expected Windows, got %s", np.Spec.Platform.AWS.ImageType), nil
			},
		},
		e2eutil.WithInterval(2*time.Second), e2eutil.WithTimeout(1*time.Minute),
	)
	t.Log("✓ Windows ImageType set successfully")

	// Test scaling operations
	it.testImageTypePersistenceThroughScaling(t, g, ctx, &defaultNodePool, *originalReplicas)

	// Restore original ImageType
	t.Logf("Restoring original ImageType: %s", originalImageType)
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&defaultNodePool), &defaultNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	defaultNodePool.Spec.Platform.AWS.ImageType = originalImageType
	err = it.mgmtClient.Update(ctx, &defaultNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to restore original ImageType")

	t.Log("✓ All NodePool ImageType scaling tests passed successfully")
}

func (it *NodePoolImageTypeTest) testImageTypePersistenceThroughScaling(t *testing.T, g *WithT, ctx context.Context, nodePool *hyperv1.NodePool, originalReplicas int32) {
	// Test 1: Scale down to 0 replicas
	t.Log("Test 1: Scaling NodePool to 0 replicas")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 0, hyperv1.ImageTypeWindows)

	// Test 2: Scale up to 2 replicas
	t.Log("Test 2: Scaling NodePool to 2 replicas")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 2, hyperv1.ImageTypeWindows)

	// Test 3: Scale down to 1 replica
	t.Log("Test 3: Scaling NodePool to 1 replica")
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, 1, hyperv1.ImageTypeWindows)

	// Test 4: Restore original replica count
	t.Logf("Test 4: Restoring original replica count to %d", originalReplicas)
	it.scaleAndVerifyImageType(t, g, ctx, nodePool, originalReplicas, hyperv1.ImageTypeWindows)
}

func (it *NodePoolImageTypeTest) scaleAndVerifyImageType(t *testing.T, g *WithT, ctx context.Context, nodePool *hyperv1.NodePool, targetReplicas int32, expectedImageType hyperv1.ImageType) {
	// Get current NodePool state
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

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
		e2eutil.WaitForNReadyNodesWithOptions(t, ctx, it.hostedClusterClient, int(targetReplicas),
			e2eutil.WithNodeReadinessTimeout(timeout),
			e2eutil.WithNodeReadinessInterval(10*time.Second),
		)
		t.Logf("✓ All %d nodes are ready", targetReplicas)
	}

	// Verify ImageType is still correct after scaling
	err = it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool after scaling")
	g.Expect(nodePool.Spec.Platform.AWS.ImageType).To(Equal(expectedImageType),
		"ImageType should persist through scaling operations")

	t.Logf("✓ Scaled to %d replicas, ImageType persisted: %s", targetReplicas, expectedImageType)
}
