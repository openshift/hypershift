package oadp

import (
	"testing"
)

func TestApplyPlatformBackupSpec(t *testing.T) {
	tests := []struct {
		name                  string
		platform              string
		expectLabelSelector   bool
	}{
		{
			name:                "When platform is KUBEVIRT it should add labelSelector",
			platform:            "KUBEVIRT",
			expectLabelSelector: true,
		},
		{
			name:                "When platform is lowercase kubevirt it should add labelSelector",
			platform:            "kubevirt",
			expectLabelSelector: true,
		},
		{
			name:                "When platform is mixed case KubeVirt it should add labelSelector",
			platform:            "KubeVirt",
			expectLabelSelector: true,
		},
		{
			name:                "When platform is AWS it should not add labelSelector",
			platform:            "AWS",
			expectLabelSelector: false,
		},
		{
			name:                "When platform is AGENT it should not add labelSelector",
			platform:            "AGENT",
			expectLabelSelector: false,
		},
		{
			name:                "When platform is empty it should not add labelSelector",
			platform:            "",
			expectLabelSelector: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := map[string]interface{}{}

			applyPlatformBackupSpec(spec, tt.platform)

			labelSelector, found := spec["labelSelector"]
			if tt.expectLabelSelector {
				if !found {
					t.Fatal("Expected labelSelector to be set, but it was not")
				}

				selectorMap, ok := labelSelector.(map[string]interface{})
				if !ok {
					t.Fatalf("Expected labelSelector to be map[string]interface{}, got %T", labelSelector)
				}

				matchExpressions, ok := selectorMap["matchExpressions"].([]interface{})
				if !ok || len(matchExpressions) != 1 {
					t.Fatalf("Expected matchExpressions with 1 entry, got %v", selectorMap["matchExpressions"])
				}

				expr, ok := matchExpressions[0].(map[string]interface{})
				if !ok {
					t.Fatalf("Expected matchExpression to be map[string]interface{}, got %T", matchExpressions[0])
				}

				if expr["key"] != "hypershift.openshift.io/is-kubevirt-rhcos" {
					t.Errorf("Expected key 'hypershift.openshift.io/is-kubevirt-rhcos', got %v", expr["key"])
				}

				if expr["operator"] != "DoesNotExist" {
					t.Errorf("Expected operator 'DoesNotExist', got %v", expr["operator"])
				}
			} else {
				if found {
					t.Errorf("Expected no labelSelector for platform %q, but got %v", tt.platform, labelSelector)
				}
			}
		})
	}
}

func TestApplyPlatformBackupSpecPreservesExistingFields(t *testing.T) {
	t.Run("When spec has existing fields it should preserve them", func(t *testing.T) {
		spec := map[string]interface{}{
			"storageLocation": "default",
			"snapshotVolumes": true,
		}

		applyPlatformBackupSpec(spec, "KUBEVIRT")

		if spec["storageLocation"] != "default" {
			t.Errorf("Expected storageLocation to be preserved, got %v", spec["storageLocation"])
		}
		if spec["snapshotVolumes"] != true {
			t.Errorf("Expected snapshotVolumes to be preserved, got %v", spec["snapshotVolumes"])
		}
		if _, found := spec["labelSelector"]; !found {
			t.Error("Expected labelSelector to be added")
		}
	})
}
