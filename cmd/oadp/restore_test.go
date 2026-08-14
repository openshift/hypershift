package oadp

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateRestoreObjectBasic(t *testing.T) {
	type testCase struct {
		name                         string
		backupName                   string
		useEtcdSnapshot              bool
		restorePVs                   bool
		expectedRestorePVs           bool
		expectedCleanupBeforeRestore bool
		expectedVeleroBackupNameRefs bool
		expectedExcludedContains     []string
		expectedExcludedNotContains  []string
	}

	tests := []testCase{
		{
			name:                         "When using default mode it should generate standard restore spec",
			backupName:                   "test-backup-123",
			useEtcdSnapshot:              false,
			restorePVs:                   true,
			expectedRestorePVs:           true,
			expectedCleanupBeforeRestore: false,
			expectedVeleroBackupNameRefs: false,
			expectedExcludedContains:     []string{"nodes", "events", "csinodes.storage.k8s.io", "volumeattachments.storage.k8s.io", "backuprepositories.velero.io"},
			expectedExcludedNotContains:  nil,
		},
		{
			name:                         "When using etcd snapshot mode it should generate etcd snapshot restore spec",
			backupName:                   "test-backup-456",
			useEtcdSnapshot:              true,
			restorePVs:                   true,
			expectedRestorePVs:           false,
			expectedCleanupBeforeRestore: true,
			expectedVeleroBackupNameRefs: true,
			expectedExcludedContains:     []string{"nodes", "events", "backups.velero.io"},
			expectedExcludedNotContains:  []string{"csinodes.storage.k8s.io", "volumeattachments.storage.k8s.io", "backuprepositories.velero.io"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &CreateOptions{
				HCName:                 "test-cluster",
				HCNamespace:            "test-cluster-ns",
				BackupName:             tt.backupName,
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "update",
				RestorePVs:             ptr.To(tt.restorePVs),
				PreserveNodePorts:      ptr.To(true),
				UseEtcdSnapshot:        tt.useEtcdSnapshot,
			}

			restore, restoreName, err := opts.GenerateRestoreObject()
			g.Expect(err).NotTo(HaveOccurred())

			// Basic properties
			g.Expect(restore.GetAPIVersion()).To(Equal("velero.io/v1"))
			g.Expect(restore.GetKind()).To(Equal("Restore"))
			g.Expect(restore.GetNamespace()).To(Equal("openshift-adp"))
			g.Expect(restoreName).To(HavePrefix("test-cluster-test-cluster-ns-"))

			// backupName
			bn, found, err := unstructured.NestedString(restore.Object, "spec", "backupName")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(bn).To(Equal(tt.backupName))

			// existingResourcePolicy
			policy, found, err := unstructured.NestedString(restore.Object, "spec", "existingResourcePolicy")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(policy).To(Equal("update"))

			// includedNamespaces
			namespaces, found, err := unstructured.NestedStringSlice(restore.Object, "spec", "includedNamespaces")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(namespaces).To(Equal([]string{"test-cluster-ns", "test-cluster-ns-test-cluster"}))

			// restorePVs
			rpv, found, err := unstructured.NestedBool(restore.Object, "spec", "restorePVs")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(rpv).To(Equal(tt.expectedRestorePVs))

			// cleanupBeforeRestore
			cbr, cbrFound, err := unstructured.NestedString(restore.Object, "spec", "cleanupBeforeRestore")
			g.Expect(err).NotTo(HaveOccurred())
			if tt.expectedCleanupBeforeRestore {
				g.Expect(cbrFound).To(BeTrue(), "cleanupBeforeRestore should be present")
				g.Expect(cbr).To(Equal("CleanupRestored"))
			} else {
				g.Expect(cbrFound).To(BeFalse(), "cleanupBeforeRestore should not be present")
			}

			// velero*BackupName refs
			for _, field := range []string{"veleroManagedClustersBackupName", "veleroCredentialsBackupName", "veleroResourcesBackupName"} {
				val, found, err := unstructured.NestedString(restore.Object, "spec", field)
				g.Expect(err).NotTo(HaveOccurred())
				if tt.expectedVeleroBackupNameRefs {
					g.Expect(found).To(BeTrue(), "%s should be present", field)
					g.Expect(val).To(Equal(tt.backupName))
				} else {
					g.Expect(found).To(BeFalse(), "%s should not be present", field)
				}
			}

			// excludedResources
			er, _, err := unstructured.NestedStringSlice(restore.Object, "spec", "excludedResources")
			g.Expect(err).NotTo(HaveOccurred())
			for _, expected := range tt.expectedExcludedContains {
				g.Expect(er).To(ContainElement(expected))
			}
			for _, notExpected := range tt.expectedExcludedNotContains {
				g.Expect(er).NotTo(ContainElement(notExpected))
			}
		})
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

	// Test restore name should be auto-generated since no custom name was provided
	// Format should be: {hcName}-{hcNamespace}-{randomSuffix}
	expectedPattern := "test-cluster-test-cluster-ns-"
	if !strings.HasPrefix(restoreName, expectedPattern) {
		t.Errorf("Expected restore name to start with '%s', got '%s'", expectedPattern, restoreName)
	}
	// Check that the name has the random suffix (should be 6 characters)
	if len(restoreName) != len(expectedPattern)+6 {
		t.Errorf("Expected restore name length to be %d, got %d", len(expectedPattern)+6, len(restoreName))
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
	namespaces := buildIncludedNamespaces(opts.HCNamespace, opts.HCName, nil)
	expected := []string{"test-cluster-ns", "test-cluster-ns-test-cluster"}
	if len(namespaces) != 2 || namespaces[0] != expected[0] || namespaces[1] != expected[1] {
		t.Errorf("buildIncludedNamespaces() = %v, want %v", namespaces, expected)
	}

	// Test additional namespaces are added to defaults
	opts.IncludeNamespaces = []string{"custom-ns1", "custom-ns2"}
	namespaces = buildIncludedNamespaces(opts.HCNamespace, opts.HCName, opts.IncludeNamespaces)
	expected = []string{"test-cluster-ns", "test-cluster-ns-test-cluster", "custom-ns1", "custom-ns2"}
	if len(namespaces) != 4 || namespaces[0] != expected[0] || namespaces[1] != expected[1] || namespaces[2] != expected[2] || namespaces[3] != expected[3] {
		t.Errorf("buildIncludedNamespaces() with additional = %v, want %v", namespaces, expected)
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

// TestValidateRestoreName verifies that restore name validation works correctly
// for custom names (--name flag), including 63-character limit and Kubernetes naming rules.
func TestValidateRestoreName(t *testing.T) {
	tests := []struct {
		name        string
		restoreName string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid short name",
			restoreName: "test-restore",
			expectError: false,
		},
		{
			name:        "Valid name with numbers",
			restoreName: "test-restore-123",
			expectError: false,
		},
		{
			name:        "Valid 63 character name",
			restoreName: "a1234567890123456789012345678901234567890123456789012345678901b",
			expectError: false,
		},
		{
			name:        "Name too long (64 characters)",
			restoreName: "a12345678901234567890123456789012345678901234567890123456789012b",
			expectError: true,
			errorMsg:    "too long (64 characters)",
		},
		{
			name:        "Name with uppercase letters",
			restoreName: "Test-restore",
			expectError: true,
			errorMsg:    "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:        "Name starting with hyphen",
			restoreName: "-test-restore",
			expectError: true,
			errorMsg:    "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:        "Name ending with hyphen",
			restoreName: "test-restore-",
			expectError: true,
			errorMsg:    "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:        "Name with invalid characters",
			restoreName: "test_restore",
			expectError: true,
			errorMsg:    "a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters",
		},
		{
			name:        "Empty name should be valid",
			restoreName: "",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				RestoreName: tt.restoreName,
			}

			err := opts.ValidateRestoreName()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for restore name '%s', but got none", tt.restoreName)
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for restore name '%s', but got: %v", tt.restoreName, err)
				}
			}
		})
	}
}
