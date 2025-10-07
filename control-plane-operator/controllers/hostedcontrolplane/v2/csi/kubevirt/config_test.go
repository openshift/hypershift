package kubevirt

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConfigMapDeterministicOrdering(t *testing.T) {
	// Test that the configmap content remains stable across multiple invocations
	// when the same storage class mappings are provided in different orders.
	// This ensures OCPBUGS-61245 is fixed.

	tests := []struct {
		name               string
		storageClassOrder  []string
		snapshotClassOrder []string
	}{
		{
			name:               "alphabetical order",
			storageClassOrder:  []string{"block-gold", "block-platinum"},
			snapshotClassOrder: []string{"block-gold", "block-platinum"},
		},
		{
			name:               "reverse alphabetical order",
			storageClassOrder:  []string{"block-platinum", "block-gold"},
			snapshotClassOrder: []string{"block-platinum", "block-gold"},
		},
		{
			name:               "mixed order",
			storageClassOrder:  []string{"block-gold", "block-platinum"},
			snapshotClassOrder: []string{"block-platinum", "block-gold"},
		},
	}

	var previousContent string

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-id",
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.ManualKubevirtStorageDriverConfigType,
								Manual: &hyperv1.KubevirtManualStorageDriverConfig{
									StorageClassMapping:        []hyperv1.KubevirtStorageClassMapping{},
									VolumeSnapshotClassMapping: []hyperv1.KubevirtVolumeSnapshotClassMapping{},
								},
							},
						},
					},
				},
			}

			// Add storage class mappings in the specified order
			for _, sc := range tt.storageClassOrder {
				hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping = append(
					hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping,
					hyperv1.KubevirtStorageClassMapping{
						Group:                 sc,
						InfraStorageClassName: sc,
					},
				)
			}

			// Add snapshot class mappings in the specified order
			for _, sc := range tt.snapshotClassOrder {
				hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping = append(
					hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping,
					hyperv1.KubevirtVolumeSnapshotClassMapping{
						Group:                        sc,
						InfraVolumeSnapshotClassName: sc,
					},
				)
			}

			cm := &corev1.ConfigMap{}
			_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
			if err != nil {
				t.Fatalf("unexpected error loading manifest: %v", err)
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			err = adaptConfigMap(cpContext, cm)
			if err != nil {
				t.Fatalf("unexpected error adapting configmap: %v", err)
			}

			content := cm.Data["infraStorageClassEnforcement"]

			// Verify the content contains expected elements
			if !strings.Contains(content, "allowAll: false") {
				t.Errorf("expected allowAll: false in content")
			}
			if !strings.Contains(content, "allowList: [block-gold, block-platinum]") {
				t.Errorf("expected sorted allowList, got: %s", content)
			}

			// Verify deterministic ordering of storageSnapshotMapping
			if !strings.Contains(content, "storageSnapshotMapping:") {
				t.Errorf("expected storageSnapshotMapping in content")
			}

			// All test cases should produce identical content
			if previousContent == "" {
				previousContent = content
			} else {
				if content != previousContent {
					t.Errorf("content is not deterministic:\nPrevious:\n%s\n\nCurrent:\n%s", previousContent, content)
				}
			}

			// Verify the mapping appears in sorted order (block-gold before block-platinum)
			goldIndex := strings.Index(content, "- storageClasses:\n  - block-gold")
			platinumIndex := strings.Index(content, "- storageClasses:\n  - block-platinum")

			if goldIndex == -1 || platinumIndex == -1 {
				t.Errorf("expected both block-gold and block-platinum in storageSnapshotMapping")
			} else if goldIndex > platinumIndex {
				t.Errorf("expected block-gold to appear before block-platinum in sorted order, got gold at %d, platinum at %d", goldIndex, platinumIndex)
			}
		})
	}
}

func TestConfigMapWithSingleStorageClass(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			Platform: hyperv1.PlatformSpec{
				Kubevirt: &hyperv1.KubevirtPlatformSpec{
					StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
						Type: hyperv1.ManualKubevirtStorageDriverConfigType,
						Manual: &hyperv1.KubevirtManualStorageDriverConfig{
							StorageClassMapping: []hyperv1.KubevirtStorageClassMapping{
								{
									Group:                 "block-gold",
									InfraStorageClassName: "block-gold",
								},
							},
							VolumeSnapshotClassMapping: []hyperv1.KubevirtVolumeSnapshotClassMapping{
								{
									Group:                        "block-gold",
									InfraVolumeSnapshotClassName: "block-gold",
								},
							},
						},
					},
				},
			},
		},
	}

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error loading manifest: %v", err)
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	err = adaptConfigMap(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error adapting configmap: %v", err)
	}

	content := cm.Data["infraStorageClassEnforcement"]

	if !strings.Contains(content, "allowList: [block-gold]") {
		t.Errorf("expected allowList: [block-gold], got: %s", content)
	}

	if !strings.Contains(content, "- storageClasses:\n  - block-gold") {
		t.Errorf("expected storageClasses with block-gold in content")
	}
}
