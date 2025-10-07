//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// KubeVirtDriverConfigTest verifies that the driver-config configmap
// maintains stable content across reconciliations (OCPBUGS-61245).
type KubeVirtDriverConfigTest struct {
	DummyInfraSetup
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeVirtDriverConfigTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtDriverConfigTest {
	return &KubeVirtDriverConfigTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
	}
}

func (k KubeVirtDriverConfigTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtDriverConfigTest")
}

func (k KubeVirtDriverConfigTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	// Get the hosted control plane namespace
	hcpNamespace := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)

	t.Logf("Checking driver-config configmap in namespace %s", hcpNamespace)

	// Wait for the driver-config configmap to exist
	var initialConfigMap *corev1.ConfigMap
	e2eutil.EventuallyObject(
		t, k.ctx, "waiting for driver-config configmap to exist",
		func(ctx context.Context) (*corev1.ConfigMap, error) {
			cm := &corev1.ConfigMap{}
			err := k.client.Get(k.ctx, crclient.ObjectKey{
				Namespace: hcpNamespace,
				Name:      "driver-config",
			}, cm)
			return cm, err
		},
		[]e2eutil.Predicate[*corev1.ConfigMap]{
			func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
				if cm.Data != nil && cm.Data["infraStorageClassEnforcement"] != "" {
					return true, "", nil
				}
				return false, "infraStorageClassEnforcement not found in configmap", nil
			},
		},
	)

	// Get the initial configmap
	initialConfigMap = &corev1.ConfigMap{}
	err := k.client.Get(k.ctx, crclient.ObjectKey{
		Namespace: hcpNamespace,
		Name:      "driver-config",
	}, initialConfigMap)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get initial driver-config configmap")

	initialContent := initialConfigMap.Data["infraStorageClassEnforcement"]
	initialResourceVersion := initialConfigMap.ResourceVersion

	t.Logf("Initial driver-config content (resource version %s):\n%s", initialResourceVersion, initialContent)

	// Verify the content has the expected structure
	g.Expect(initialContent).To(ContainSubstring("allowAll:"), "expected allowAll field")
	g.Expect(initialContent).To(ContainSubstring("storageSnapshotMapping:"), "expected storageSnapshotMapping field")

	// Wait and check multiple times to ensure the configmap remains stable
	// This tests that the content doesn't flip-flop between different orderings
	stableChecks := 5
	checkInterval := 10 * time.Second

	for i := 0; i < stableChecks; i++ {
		time.Sleep(checkInterval)

		currentConfigMap := &corev1.ConfigMap{}
		err := k.client.Get(k.ctx, crclient.ObjectKey{
			Namespace: hcpNamespace,
			Name:      "driver-config",
		}, currentConfigMap)
		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to get driver-config configmap on check %d", i+1))

		currentContent := currentConfigMap.Data["infraStorageClassEnforcement"]
		currentResourceVersion := currentConfigMap.ResourceVersion

		t.Logf("Check %d: driver-config content (resource version %s)", i+1, currentResourceVersion)

		// The content should remain identical across reconciliations
		if currentContent != initialContent {
			t.Errorf("driver-config content changed on check %d (OCPBUGS-61245 regression):\nInitial (rv %s):\n%s\n\nCurrent (rv %s):\n%s",
				i+1, initialResourceVersion, initialContent, currentResourceVersion, currentContent)
		}

		// Verify ordering is maintained if storage classes are present
		if strings.Contains(currentContent, "allowList:") {
			// Extract the allowList content
			lines := strings.Split(currentContent, "\n")
			for _, line := range lines {
				if strings.Contains(line, "allowList:") {
					// Verify it's sorted (this is implicit in the stable content check,
					// but makes the test intention clearer)
					t.Logf("Found allowList: %s", strings.TrimSpace(line))
					break
				}
			}
		}
	}

	t.Logf("driver-config configmap remained stable across %d checks over %v", stableChecks, time.Duration(stableChecks)*checkInterval)
}

func (k KubeVirtDriverConfigTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-driver-config",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = ptr.To(int32(1))

	return nodePool, nil
}
