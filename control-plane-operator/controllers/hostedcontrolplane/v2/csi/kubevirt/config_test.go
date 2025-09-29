package kubevirt

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptConfigMapConsistentOrdering(t *testing.T) {
	// Test that configmap content is consistent across multiple calls with the same input
	// This addresses OCPBUGS-61245 where driver-config content was flapping due to random map iteration

	// Create a test HCP with multiple storage class mappings in different orders
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.KubevirtPlatform,
				Kubevirt: &hyperv1.KubevirtPlatformSpec{
					StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
						Type: hyperv1.ManualKubevirtStorageDriverConfigType,
						Manual: &hyperv1.KubevirtManualStorageDriverConfig{
							StorageClassMapping: []hyperv1.KubevirtStorageClassMapping{
								{Group: "group-b", InfraStorageClassName: "block-platinum"},
								{Group: "group-a", InfraStorageClassName: "block-gold"},
								{Group: "group-b", InfraStorageClassName: "block-silver"},
								{Group: "group-a", InfraStorageClassName: "block-bronze"},
							},
							VolumeSnapshotClassMapping: []hyperv1.KubevirtVolumeSnapshotClassMapping{
								{Group: "group-b", InfraVolumeSnapshotClassName: "snap-platinum"},
								{Group: "group-a", InfraVolumeSnapshotClassName: "snap-gold"},
								{Group: "group-b", InfraVolumeSnapshotClassName: "snap-silver"},
								{Group: "group-a", InfraVolumeSnapshotClassName: "snap-bronze"},
							},
						},
					},
				},
			},
		},
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	// Run the function multiple times and check that the output is consistent
	configs := make([]string, 10)
	for i := 0; i < 10; i++ {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "driver-config",
				Namespace: "test-namespace",
			},
		}

		err := adaptConfigMap(cpContext, cm)
		if err != nil {
			t.Fatalf("adaptConfigMap failed: %v", err)
		}

		configs[i] = cm.Data["infraStorageClassEnforcement"]
	}

	// All configs should be identical
	firstConfig := configs[0]
	for i, config := range configs {
		if config != firstConfig {
			t.Errorf("Configuration %d differs from the first one:\nFirst: %s\nCurrent: %s", i, firstConfig, config)
		}
	}

	// Verify that the content has proper sorting
	config := firstConfig

	// Check that allowList is sorted
	if !strings.Contains(config, "allowList: [block-bronze, block-gold, block-platinum, block-silver]") {
		t.Errorf("allowList is not properly sorted in config: %s", config)
	}

	// Check that the mapping contains both groups in sorted order
	if !strings.Contains(config, "storageSnapshotMapping:") {
		t.Errorf("storageSnapshotMapping not found in config: %s", config)
	}

	// Verify group-a appears before group-b in the YAML (alphabetical order)
	groupAIndex := strings.Index(config, "snap-bronze")
	groupBIndex := strings.Index(config, "snap-platinum")
	if groupAIndex == -1 || groupBIndex == -1 {
		t.Errorf("Could not find snapshot class names in config: %s", config)
	}
	if groupAIndex > groupBIndex {
		t.Errorf("Groups are not in alphabetical order. group-a should appear before group-b in config: %s", config)
	}
}

func TestAdaptConfigMapEmptyMappings(t *testing.T) {
	// Test with no mappings to ensure it doesn't break
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.KubevirtPlatform,
				Kubevirt: &hyperv1.KubevirtPlatformSpec{
					StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
						Type:   hyperv1.ManualKubevirtStorageDriverConfigType,
						Manual: &hyperv1.KubevirtManualStorageDriverConfig{},
					},
				},
			},
		},
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "driver-config",
			Namespace: "test-namespace",
		},
	}

	err := adaptConfigMap(cpContext, cm)
	if err != nil {
		t.Fatalf("adaptConfigMap failed with empty mappings: %v", err)
	}

	config := cm.Data["infraStorageClassEnforcement"]
	expected := "allowAll: false\nallowList: []\nstorageSnapshotMapping: \n[]\n"
	if config != expected {
		t.Errorf("Expected empty config:\n%s\nGot:\n%s", expected, config)
	}
}
