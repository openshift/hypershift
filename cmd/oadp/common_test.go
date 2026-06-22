package oadp

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestApplyPlatformBackupSpec(t *testing.T) {
	tests := []struct {
		name                string
		platform            string
		expectLabelSelector bool
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

func TestGenerateResourcePolicyConfigMap(t *testing.T) {
	t.Run("When generating a resource policy ConfigMap it should have correct structure", func(t *testing.T) {
		cm := GenerateResourcePolicyConfigMap("test-policy", "openshift-adp")

		if cm.GetKind() != "ConfigMap" {
			t.Errorf("Expected kind 'ConfigMap', got %s", cm.GetKind())
		}
		if cm.GetAPIVersion() != "v1" {
			t.Errorf("Expected apiVersion 'v1', got %s", cm.GetAPIVersion())
		}
		if cm.GetName() != "test-policy" {
			t.Errorf("Expected name 'test-policy', got %s", cm.GetName())
		}
		if cm.GetNamespace() != "openshift-adp" {
			t.Errorf("Expected namespace 'openshift-adp', got %s", cm.GetNamespace())
		}

		data, found, err := unstructured.NestedString(cm.Object, "data", "policies.yaml")
		if err != nil || !found {
			t.Fatal("Expected data.policies.yaml to be set")
		}
		if !strings.Contains(data, "hypershift.openshift.io/is-kubevirt-rhcos") {
			t.Error("Expected policies.yaml to contain the RHCOS label name")
		}
		if !strings.Contains(data, "type: skip") {
			t.Error("Expected policies.yaml to contain skip action")
		}
		if !strings.Contains(data, "volumePolicies") {
			t.Error("Expected policies.yaml to contain volumePolicies")
		}
	})
}

func TestGenerateResourcePolicyName(t *testing.T) {
	t.Run("When generating a resource policy name it should follow the naming pattern", func(t *testing.T) {
		name := GenerateResourcePolicyName("test-cluster", "test-ns")
		expectedPrefix := "test-cluster-test-ns-"
		if !strings.HasPrefix(name, expectedPrefix) {
			t.Errorf("Expected name to start with '%s', got '%s'", expectedPrefix, name)
		}
		if len(name) != len(expectedPrefix)+6 {
			t.Errorf("Expected name length %d, got %d", len(expectedPrefix)+6, len(name))
		}
	})

	t.Run("When called with the same inputs it should return the same name", func(t *testing.T) {
		name1 := GenerateResourcePolicyName("test-cluster", "test-ns")
		name2 := GenerateResourcePolicyName("test-cluster", "test-ns")
		if name1 != name2 {
			t.Errorf("Expected deterministic name, got '%s' and '%s'", name1, name2)
		}
	})

	t.Run("When called with different inputs it should return different names", func(t *testing.T) {
		name1 := GenerateResourcePolicyName("cluster-a", "ns-a")
		name2 := GenerateResourcePolicyName("cluster-b", "ns-b")
		if name1 == name2 {
			t.Errorf("Expected different names for different inputs, both got '%s'", name1)
		}
	})

	t.Run("When HC name and namespace exceed 63 chars it should shorten properly", func(t *testing.T) {
		name := GenerateResourcePolicyName("very-long-hostedcluster-name-12345", "very-long-namespace-name-12345")
		if len(name) > 63 {
			t.Errorf("Expected name <= 63 chars, got %d: %s", len(name), name)
		}
		if len(name) == 0 {
			t.Error("Expected non-empty name")
		}
	})
}

func TestIsKubeVirtPlatform(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		expected bool
	}{
		{"When platform is KUBEVIRT it should return true", "KUBEVIRT", true},
		{"When platform is lowercase kubevirt it should return true", "kubevirt", true},
		{"When platform is mixed case KubeVirt it should return true", "KubeVirt", true},
		{"When platform is AWS it should return false", "AWS", false},
		{"When platform is empty it should return false", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKubeVirtPlatform(tt.platform); got != tt.expected {
				t.Errorf("isKubeVirtPlatform(%q) = %v, want %v", tt.platform, got, tt.expected)
			}
		})
	}
}

func TestApplyResourcePolicy(t *testing.T) {
	t.Run("When applying resource policy it should set configmap reference", func(t *testing.T) {
		spec := map[string]interface{}{}
		applyResourcePolicy(spec, "my-policy")

		rp, found := spec["resourcePolicy"]
		if !found {
			t.Fatal("Expected resourcePolicy to be set")
		}
		rpMap, ok := rp.(map[string]interface{})
		if !ok {
			t.Fatalf("Expected resourcePolicy to be map, got %T", rp)
		}
		if rpMap["kind"] != "configmap" {
			t.Errorf("Expected kind 'configmap', got %v", rpMap["kind"])
		}
		if rpMap["name"] != "my-policy" {
			t.Errorf("Expected name 'my-policy', got %v", rpMap["name"])
		}
	})
}

func TestValidateEtcdSnapshotFlags(t *testing.T) {
	tests := []struct {
		name                     string
		useEtcdSnapshot          bool
		snapshotMoveData         bool
		defaultVolumesToFsBackup bool
		changedFlags             map[string]bool // flags explicitly set by the user
		expectErr                bool
		errMsg                   string
	}{
		{
			name:                     "When etcd snapshot is disabled it should accept any flags",
			useEtcdSnapshot:          false,
			snapshotMoveData:         true,
			defaultVolumesToFsBackup: true,
			changedFlags:             map[string]bool{"snapshot-move-data": true, "default-volumes-to-fs-backup": true},
			expectErr:                false,
		},
		{
			name:             "When etcd snapshot is enabled without explicit conflicting flags it should pass",
			useEtcdSnapshot:  true,
			snapshotMoveData: true, // default value, but not explicitly changed
			changedFlags:     map[string]bool{"use-etcd-snapshot": true},
			expectErr:        false,
		},
		{
			name:             "When etcd snapshot is enabled with explicit snapshot-move-data it should return error",
			useEtcdSnapshot:  true,
			snapshotMoveData: true,
			changedFlags:     map[string]bool{"use-etcd-snapshot": true, "snapshot-move-data": true},
			expectErr:        true,
			errMsg:           "--snapshot-move-data cannot be used with --use-etcd-snapshot",
		},
		{
			name:                     "When etcd snapshot is enabled with explicit default-volumes-to-fs-backup it should return error",
			useEtcdSnapshot:          true,
			defaultVolumesToFsBackup: true,
			changedFlags:             map[string]bool{"use-etcd-snapshot": true, "default-volumes-to-fs-backup": true},
			expectErr:                true,
			errMsg:                   "--default-volumes-to-fs-backup cannot be used with --use-etcd-snapshot",
		},
		{
			name:            "When etcd snapshot is enabled with explicit restore-pvs it should return error",
			useEtcdSnapshot: true,
			changedFlags:    map[string]bool{"use-etcd-snapshot": true, "restore-pvs": true},
			expectErr:       true,
			errMsg:          "--restore-pvs cannot be used with --use-etcd-snapshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &CreateOptions{
				UseEtcdSnapshot:          tt.useEtcdSnapshot,
				SnapshotMoveData:         tt.snapshotMoveData,
				DefaultVolumesToFsBackup: tt.defaultVolumesToFsBackup,
			}

			err := opts.validateEtcdSnapshotFlags(tt.changedFlags)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errMsg))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
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
