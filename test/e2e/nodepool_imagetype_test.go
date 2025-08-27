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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
		Strategy: hyperv1.UpgradeStrategyRollingUpdate,
		RollingUpdate: &hyperv1.RollingUpdate{
			MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
			MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(oneReplicas))),
		},
	}
	nodePool.Spec.Platform.AWS.InstanceType = "m5.metal"

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	ctx := it.ctx

	t.Run("TestDefaultImageType", func(t *testing.T) {
		it.testDefaultImageType(t, ctx, nodePool, nodes)
	})

	t.Run("TestWindowsImageType", func(t *testing.T) {
		it.testWindowsImageType(t, ctx, nodePool, nodes)
	})

	t.Run("TestInvalidImageType", func(t *testing.T) {
		it.testInvalidImageType(t, ctx, nodePool)
	})
}

// testDefaultImageType verifies that leaving ImageType empty uses default Linux/RHCOS AMI
func (it *NodePoolImageTypeTest) testDefaultImageType(t *testing.T, ctx context.Context, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("Testing default ImageType (empty)")

	// Create nodepool without ImageType set (defaults to empty)
	np := nodePool.DeepCopy()
	np.Name = it.hostedCluster.Name + "-default-imagetype"
	np.ResourceVersion = ""             // Clear resourceVersion for object creation
	np.Spec.Platform.AWS.ImageType = "" // Explicitly set to Linux

	if err := it.mgmtClient.Create(ctx, np); err != nil {
		t.Fatalf("failed to create nodepool with default ImageType: %v", err)
	}
	defer func() {
		if err := it.mgmtClient.Delete(ctx, np); err != nil {
			t.Logf("failed to delete nodepool: %v", err)
		}
	}()

	// wait for the nodepool status conditions to populate
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("nodepool %s/%s to have a populated PlatformImage status condition", np.Namespace, np.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type: hyperv1.NodePoolValidPlatformImageType,
			}),
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
	)

	// Check that the ValidPlatformImageType condition is True
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(np.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	if validImageCondition == nil {
		t.Fatalf("ValidPlatformImageType condition not found")
	}
	if validImageCondition.Status != corev1.ConditionTrue {
		t.Fatalf("expected ValidPlatformImageType condition to be True, got %s: %s", validImageCondition.Status, validImageCondition.Message)
	}

	// Verify message contains AMI information (not Windows-specific)
	if strings.Contains(strings.ToLower(validImageCondition.Message), "windows") {
		t.Fatalf("default ImageType should not use Windows AMI, but condition message contains 'windows': %s", validImageCondition.Message)
	}

	t.Log("Default ImageType test passed")
}

// testWindowsImageType verifies that setting ImageType to "windows" uses Windows AMI
func (it *NodePoolImageTypeTest) testWindowsImageType(t *testing.T, ctx context.Context, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("Testing Windows ImageType")

	// Create nodepool with Windows ImageType
	np := nodePool.DeepCopy()
	np.Name = it.hostedCluster.Name + "-windows-imagetype"
	np.ResourceVersion = "" // Clear resourceVersion for object creation
	np.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows

	if err := it.mgmtClient.Create(ctx, np); err != nil {
		t.Fatalf("failed to create nodepool with Windows ImageType: %v", err)
	}
	defer func() {
		if err := it.mgmtClient.Delete(ctx, np); err != nil {
			t.Logf("failed to delete nodepool: %v", err)
		}
	}()

	// wait for the nodepool status conditions to populate
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s to have a populated PlatformImage status condition", np.Namespace, np.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), np)
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
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(np.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
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

// testInvalidImageType verifies that using an invalid ImageType value fails validation
func (it *NodePoolImageTypeTest) testInvalidImageType(t *testing.T, ctx context.Context, nodePool hyperv1.NodePool) {
	t.Log("Testing invalid ImageType")

	// Create nodepool with invalid ImageType - this should fail at API level due to enum validation
	np := nodePool.DeepCopy()
	np.Name = it.hostedCluster.Name + "-invalid-imagetype"
	np.ResourceVersion = ""                         // Clear resourceVersion for object creation
	np.Spec.Platform.AWS.ImageType = "invalid-type" // This should be rejected by the API

	err := it.mgmtClient.Create(ctx, np)
	if err == nil {
		// Clean up if somehow it was created
		it.mgmtClient.Delete(ctx, np)
		t.Fatalf("expected creation of nodepool with invalid ImageType to fail, but it succeeded")
	}

	// Verify the error is related to validation
	if !strings.Contains(err.Error(), "enum") && !strings.Contains(err.Error(), "validation") {
		t.Fatalf("expected validation error for invalid ImageType, but got: %v", err)
	}

	t.Log("Invalid ImageType test passed - creation failed as expected")
}
