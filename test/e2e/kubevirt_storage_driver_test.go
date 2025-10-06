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
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestKubeVirtStorageDriverConfig validates that the driver-config ConfigMap
// has consistent ordering and doesn't flap between reconciliations.
// This test addresses OCPBUGS-61245 where the ConfigMap content was changing
// order on each reconciliation due to random map iteration.
func TestKubeVirtStorageDriverConfig(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Get the hosted control plane namespace
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// Get the driver-config ConfigMap
		driverConfigCM := hcpmanifests.KubevirtCSIDriverInfraConfigMap(hcpNamespace)

		t.Logf("Waiting for driver-config ConfigMap to exist in namespace %s", hcpNamespace)
		e2eutil.EventuallyObject(
			t, ctx, fmt.Sprintf("driver-config ConfigMap to exist in %s", hcpNamespace),
			func(ctx context.Context) (*corev1.ConfigMap, error) {
				cm := &corev1.ConfigMap{}
				err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: driverConfigCM.Name}, cm)
				return cm, err
			},
			[]e2eutil.Predicate[*corev1.ConfigMap]{
				func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
					if cm.Data == nil || cm.Data["infraStorageClassEnforcement"] == "" {
						return false, "ConfigMap data not populated yet", nil
					}
					return true, "", nil
				},
			},
		)

		// Capture the initial ConfigMap content
		initialCM := &corev1.ConfigMap{}
		err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: driverConfigCM.Name}, initialCM)
		g.Expect(err).ShouldNot(HaveOccurred())
		initialContent := initialCM.Data["infraStorageClassEnforcement"]

		t.Logf("Initial driver-config content:\n%s", initialContent)

		// Verify the content has proper alphabetical ordering
		verifyContentOrdering(t, g, initialContent)

		// Wait and check multiple times to ensure the content doesn't flap
		// We'll check 5 times over 30 seconds to ensure consistency
		t.Log("Verifying ConfigMap content remains consistent across multiple reconciliations")
		for i := 0; i < 5; i++ {
			time.Sleep(6 * time.Second)

			currentCM := &corev1.ConfigMap{}
			err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: driverConfigCM.Name}, currentCM)
			g.Expect(err).ShouldNot(HaveOccurred())

			currentContent := currentCM.Data["infraStorageClassEnforcement"]

			// The content should be exactly the same as the initial content
			if currentContent != initialContent {
				t.Errorf("ConfigMap content changed on check %d.\nInitial:\n%s\n\nCurrent:\n%s",
					i+1, initialContent, currentContent)
			}

			t.Logf("Check %d: ConfigMap content is consistent", i+1)
		}

		t.Log("Successfully verified driver-config ConfigMap has consistent ordering")
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "kubevirt-storage-driver", globalOpts.ServiceAccountSigningKey)
}

// verifyContentOrdering checks that the ConfigMap content has alphabetically sorted elements
func verifyContentOrdering(t *testing.T, g Gomega, content string) {
	// Check that the content contains sorted elements
	// The allowList should be alphabetically sorted
	if strings.Contains(content, "allowList:") {
		t.Log("Verifying allowList is alphabetically sorted")
		// Extract the allowList line
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "allowList:") {
				// The format is "allowList: [item1, item2, item3]"
				listPart := strings.TrimPrefix(line, "allowList:")
				listPart = strings.Trim(listPart, " []")
				if listPart != "" {
					items := strings.Split(listPart, ", ")
					// Verify items are sorted
					for i := 1; i < len(items); i++ {
						if items[i-1] > items[i] {
							t.Errorf("allowList is not sorted: %s comes after %s", items[i-1], items[i])
						}
					}
					t.Logf("allowList is properly sorted: [%s]", listPart)
				}
				break
			}
		}
	}

	// Check that storage snapshot mappings are present and properly formatted
	if strings.Contains(content, "storageSnapshotMapping:") {
		t.Log("Found storageSnapshotMapping in ConfigMap")

		// Verify that storageClasses and volumeSnapshotClasses keys are lowercase
		// (as enforced by the byte replacement in the code)
		g.Expect(content).Should(ContainSubstring("storageClasses:"),
			"storageClasses key should be lowercase")
		g.Expect(content).Should(ContainSubstring("volumeSnapshotClasses:"),
			"volumeSnapshotClasses key should be lowercase")

		// Should not contain uppercase versions
		g.Expect(content).ShouldNot(ContainSubstring("StorageClasses:"),
			"StorageClasses should not be uppercase")
		g.Expect(content).ShouldNot(ContainSubstring("VolumeSnapshotClasses:"),
			"VolumeSnapshotClasses should not be uppercase")
	}
}
