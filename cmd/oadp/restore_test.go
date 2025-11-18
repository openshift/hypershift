package oadp

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateRestoreObjectBasic(t *testing.T) {
	opts := &CreateOptions{
		HCName:                 "test-cluster",
		HCNamespace:            "test-cluster-ns",
		BackupName:             "test-backup-123",
		OADPNamespace:          "openshift-adp",
		ExistingResourcePolicy: "update",
		RestorePVs:             ptr.To(true),
		PreserveNodePorts:      ptr.To(true),
	}

	restore, restoreName, err := opts.GenerateRestoreObject()
	if err != nil {
		t.Fatalf("GenerateRestoreObject() failed: %v", err)
	}

	// Test basic properties
	if restore.GetAPIVersion() != "velero.io/v1" {
		t.Errorf("Expected APIVersion 'velero.io/v1', got '%s'", restore.GetAPIVersion())
	}

	if restore.GetKind() != "Restore" {
		t.Errorf("Expected Kind 'Restore', got '%s'", restore.GetKind())
	}

	if restore.GetNamespace() != "openshift-adp" {
		t.Errorf("Expected namespace 'openshift-adp', got '%s'", restore.GetNamespace())
	}

	if restoreName == "" {
		t.Error("restore name should not be empty")
	}

	if !strings.Contains(restoreName, "test-backup-123") {
		t.Errorf("restore name should contain backup name, got: %s", restoreName)
	}

	if !strings.Contains(restoreName, "test-cluster") {
		t.Errorf("restore name should contain cluster name, got: %s", restoreName)
	}

	// Test spec fields
	backupName, found, err := unstructured.NestedString(restore.Object, "spec", "backupName")
	if err != nil {
		t.Errorf("Failed to get backupName: %v", err)
	}
	if !found || backupName != "test-backup-123" {
		t.Errorf("Expected backupName 'test-backup-123', got '%s'", backupName)
	}

	policy, found, err := unstructured.NestedString(restore.Object, "spec", "existingResourcePolicy")
	if err != nil {
		t.Errorf("Failed to get existingResourcePolicy: %v", err)
	}
	if !found || policy != "update" {
		t.Errorf("Expected existingResourcePolicy 'update', got '%s'", policy)
	}

	// Test included namespaces
	namespaces, found, err := unstructured.NestedStringSlice(restore.Object, "spec", "includedNamespaces")
	if err != nil {
		t.Errorf("Failed to get includedNamespaces: %v", err)
	}
	if !found || len(namespaces) != 2 {
		t.Errorf("Expected 2 included namespaces, got %d", len(namespaces))
	}
	expectedNamespaces := []string{"test-cluster-ns", "test-cluster-ns-test-cluster"}
	if len(namespaces) == 2 && (namespaces[0] != expectedNamespaces[0] || namespaces[1] != expectedNamespaces[1]) {
		t.Errorf("Expected namespaces %v, got %v", expectedNamespaces, namespaces)
	}
}

func TestGenerateRestoreObjectWithSchedule(t *testing.T) {
	opts := &CreateOptions{
		HCName:                 "test-cluster",
		HCNamespace:            "test-cluster-ns",
		ScheduleName:           "test-schedule-123",
		OADPNamespace:          "openshift-adp",
		ExistingResourcePolicy: "none",
		RestorePVs:             ptr.To(false),
		PreserveNodePorts:      ptr.To(false),
	}

	restore, restoreName, err := opts.GenerateRestoreObject()
	if err != nil {
		t.Fatalf("GenerateRestoreObject() failed: %v", err)
	}

	// Test schedule name
	scheduleName, found, err := unstructured.NestedString(restore.Object, "spec", "scheduleName")
	if err != nil {
		t.Errorf("Failed to get scheduleName: %v", err)
	}
	if !found || scheduleName != "test-schedule-123" {
		t.Errorf("Expected scheduleName 'test-schedule-123', got '%s'", scheduleName)
	}

	// Backup name should not be set
	backupName, found, err := unstructured.NestedString(restore.Object, "spec", "backupName")
	if err != nil {
		t.Errorf("Failed to get backupName: %v", err)
	}
	if found && backupName != "" {
		t.Errorf("Expected empty backupName when using schedule, got '%s'", backupName)
	}

	// Test restore name contains schedule
	if !strings.Contains(restoreName, "test-schedule-123") {
		t.Errorf("restore name should contain schedule name, got: %s", restoreName)
	}
}

func TestValidateBackupOrSchedule(t *testing.T) {
	// Test valid backup
	opts := &CreateOptions{BackupName: "test-backup"}
	err := opts.validateBackupOrSchedule()
	if err != nil {
		t.Errorf("validateBackupOrSchedule() should succeed with backup name, got error: %v", err)
	}

	// Test valid schedule
	opts = &CreateOptions{ScheduleName: "test-schedule"}
	err = opts.validateBackupOrSchedule()
	if err != nil {
		t.Errorf("validateBackupOrSchedule() should succeed with schedule name, got error: %v", err)
	}

	// Test neither backup nor schedule
	opts = &CreateOptions{}
	err = opts.validateBackupOrSchedule()
	if err == nil {
		t.Error("validateBackupOrSchedule() should fail when neither backup nor schedule is specified")
	}

	// Test both backup and schedule
	opts = &CreateOptions{BackupName: "backup", ScheduleName: "schedule"}
	err = opts.validateBackupOrSchedule()
	if err == nil {
		t.Error("validateBackupOrSchedule() should fail when both backup and schedule are specified")
	}
}

func TestValidateExistingResourcePolicy(t *testing.T) {
	// Test valid policies
	validPolicies := []string{"none", "update"}
	for _, policy := range validPolicies {
		opts := &CreateOptions{ExistingResourcePolicy: policy}
		err := opts.validateExistingResourcePolicy()
		if err != nil {
			t.Errorf("validateExistingResourcePolicy() should accept policy '%s', got error: %v", policy, err)
		}
	}

	// Test invalid policy
	opts := &CreateOptions{ExistingResourcePolicy: "invalid"}
	err := opts.validateExistingResourcePolicy()
	if err == nil {
		t.Error("validateExistingResourcePolicy() should reject invalid policy")
	}
}

func TestBuildIncludedNamespaces(t *testing.T) {
	// Test default namespaces
	opts := &CreateOptions{
		HCName:      "test-cluster",
		HCNamespace: "test-cluster-ns",
	}
	namespaces := opts.buildIncludedNamespaces()
	expected := []string{"test-cluster-ns", "test-cluster-ns-test-cluster"}
	if len(namespaces) != 2 || namespaces[0] != expected[0] || namespaces[1] != expected[1] {
		t.Errorf("buildIncludedNamespaces() = %v, want %v", namespaces, expected)
	}

	// Test custom namespaces override
	opts.IncludeNamespaces = []string{"custom-ns1", "custom-ns2"}
	namespaces = opts.buildIncludedNamespaces()
	expected = []string{"custom-ns1", "custom-ns2"}
	if len(namespaces) != 2 || namespaces[0] != expected[0] || namespaces[1] != expected[1] {
		t.Errorf("buildIncludedNamespaces() with custom = %v, want %v", namespaces, expected)
	}
}

func TestValidateBackupExistsWithPhaseValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		backupPhase string
		renderMode  bool
		shouldError bool
	}{
		{"completed backup normal mode", "Completed", false, false},
		{"completed backup render mode", "Completed", true, false},
		{"failed backup normal mode", "Failed", false, true},
		{"failed backup render mode", "Failed", true, false},
		{"inprogress backup normal mode", "InProgress", false, true},
		{"inprogress backup render mode", "InProgress", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a backup object with the specified phase
			backup := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "velero.io/v1",
					"kind":       "Backup",
					"metadata": map[string]interface{}{
						"name":      "test-backup",
						"namespace": "openshift-adp",
					},
					"status": map[string]interface{}{
						"phase": tt.backupPhase,
					},
				},
			}

			// Create a fake client with the backup object
			scheme := runtime.NewScheme()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(backup).
				Build()

			opts := &CreateOptions{
				BackupName:    "test-backup",
				OADPNamespace: "openshift-adp",
				Client:        fakeClient,
			}

			// Test the validation function
			err := opts.validateBackupExists(ctx, tt.renderMode)

			// Check error expectation
			if tt.shouldError && err == nil {
				t.Errorf("expected error, but got none")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("expected no error, but got: %v", err)
			}
		})
	}
}

func TestValidateBackupExistsNotFound(t *testing.T) {
	ctx := context.Background()

	// Create a fake client with no backup objects
	scheme := runtime.NewScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	opts := &CreateOptions{
		BackupName:    "non-existent-backup",
		OADPNamespace: "openshift-adp",
		Client:        fakeClient,
	}

	// Test both render modes - backup not found should always error
	for _, renderMode := range []bool{false, true} {
		err := opts.validateBackupExists(ctx, renderMode)
		if err == nil {
			t.Errorf("validateBackupExists(renderMode=%t) should error when backup not found", renderMode)
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error should mention 'not found', got: %v", err)
		}
	}
}
