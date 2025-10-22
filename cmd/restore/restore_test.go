package restore

import (
	"fmt"
	"reflect"
	"testing"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func TestGenerateRestoreName(t *testing.T) {
	tests := []struct {
		name           string
		sourceName     string
		hcName         string
		randomGen      func(int) string
		expectedResult string
	}{
		{
			name:       "backup restore name generation",
			sourceName: "test-backup",
			hcName:     "my-cluster",
			randomGen: func(n int) string {
				return "abc123"
			},
			expectedResult: "test-backup-my-cluster-restore-abc123",
		},
		{
			name:       "schedule restore name generation",
			sourceName: "daily-schedule",
			hcName:     "prod-cluster",
			randomGen: func(n int) string {
				return "def456"
			},
			expectedResult: "daily-schedule-prod-cluster-restore-def456",
		},
		{
			name:       "source name with hyphens",
			sourceName: "my-cluster-backup-xyz",
			hcName:     "test-hc",
			randomGen: func(n int) string {
				return "ghi789"
			},
			expectedResult: "my-cluster-backup-xyz-test-hc-restore-ghi789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateRestoreName(tt.sourceName, tt.hcName, tt.randomGen)
			if result != tt.expectedResult {
				t.Errorf("generateRestoreName() = %v, want %v", result, tt.expectedResult)
			}
		})
	}
}

func TestCreateOptions_validateBackupOrSchedule(t *testing.T) {
	tests := []struct {
		name         string
		backupName   string
		scheduleName string
		wantErr      bool
		expectedErr  string
	}{
		{
			name:         "valid backup only",
			backupName:   "test-backup",
			scheduleName: "",
			wantErr:      false,
		},
		{
			name:         "valid schedule only",
			backupName:   "",
			scheduleName: "test-schedule",
			wantErr:      false,
		},
		{
			name:         "both backup and schedule",
			backupName:   "test-backup",
			scheduleName: "test-schedule",
			wantErr:      true,
			expectedErr:  "mutually exclusive",
		},
		{
			name:         "neither backup nor schedule",
			backupName:   "",
			scheduleName: "",
			wantErr:      true,
			expectedErr:  "either --from-backup or --from-schedule must be specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &CreateOptions{
				BackupName:   tt.backupName,
				ScheduleName: tt.scheduleName,
			}
			err := o.validateBackupOrSchedule()
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOptions.validateBackupOrSchedule() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.expectedErr != "" {
				if !contains(err.Error(), tt.expectedErr) {
					t.Errorf("CreateOptions.validateBackupOrSchedule() error = %v, should contain %v", err, tt.expectedErr)
				}
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCreateOptions_validateExistingResourcePolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		wantErr bool
	}{
		{
			name:    "valid policy - update",
			policy:  "update",
			wantErr: false,
		},
		{
			name:    "valid policy - none",
			policy:  "none",
			wantErr: false,
		},
		{
			name:    "invalid policy",
			policy:  "invalid",
			wantErr: true,
		},
		{
			name:    "empty policy",
			policy:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &CreateOptions{
				ExistingResourcePolicy: tt.policy,
			}
			err := o.validateExistingResourcePolicy()
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOptions.validateExistingResourcePolicy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateOptions_buildIncludedNamespaces(t *testing.T) {
	tests := []struct {
		name              string
		hcName            string
		hcNamespace       string
		includeNamespaces []string
		expected          []string
	}{
		{
			name:              "basic namespaces (default)",
			hcName:            "test-cluster",
			hcNamespace:       "clusters",
			includeNamespaces: nil,
			expected:          []string{"clusters", "clusters-test-cluster"},
		},
		{
			name:              "override with custom namespaces",
			hcName:            "my-cluster",
			hcNamespace:       "hosting",
			includeNamespaces: []string{"custom-ns1", "custom-ns2"},
			expected:          []string{"custom-ns1", "custom-ns2"},
		},
		{
			name:              "override with single namespace",
			hcName:            "dup-cluster",
			hcNamespace:       "test-ns",
			includeNamespaces: []string{"override-ns"},
			expected:          []string{"override-ns"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &CreateOptions{
				HCName:            tt.hcName,
				HCNamespace:       tt.hcNamespace,
				IncludeNamespaces: tt.includeNamespaces,
			}
			result := o.buildIncludedNamespaces()
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("CreateOptions.buildIncludedNamespaces() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCreateOptions_generateRestoreObject(t *testing.T) {
	tests := []struct {
		name    string
		opts    *CreateOptions
		wantErr bool
		check   func(*velerov1.Restore) error
	}{
		{
			name: "basic restore object generation",
			opts: &CreateOptions{
				HCName:                 "test-cluster",
				HCNamespace:            "clusters",
				BackupName:             "test-backup",
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "update",
				RestorePVs:             true,
				PreserveNodePorts:      true,
			},
			wantErr: false,
			check: func(restore *velerov1.Restore) error {
				if restore.Spec.BackupName != "test-backup" {
					return errorf("expected backup name 'test-backup', got '%s'", restore.Spec.BackupName)
				}
				if restore.Spec.ExistingResourcePolicy != "update" {
					return errorf("expected existing resource policy 'update', got '%s'", restore.Spec.ExistingResourcePolicy)
				}
				if restore.Spec.RestorePVs == nil || !*restore.Spec.RestorePVs {
					return errorf("expected RestorePVs to be true")
				}
				if restore.Spec.PreserveNodePorts == nil || !*restore.Spec.PreserveNodePorts {
					return errorf("expected PreserveNodePorts to be true")
				}
				expectedNamespaces := []string{"clusters", "clusters-test-cluster"}
				if !reflect.DeepEqual(restore.Spec.IncludedNamespaces, expectedNamespaces) {
					return errorf("expected namespaces %v, got %v", expectedNamespaces, restore.Spec.IncludedNamespaces)
				}
				if len(restore.Spec.ExcludedResources) == 0 {
					return errorf("expected excluded resources to be set")
				}
				return nil
			},
		},
		{
			name: "restore with overridden namespaces",
			opts: &CreateOptions{
				HCName:                 "my-cluster",
				HCNamespace:            "hosting",
				BackupName:             "my-backup",
				OADPNamespace:          "oadp-ns",
				ExistingResourcePolicy: "none",
				IncludeNamespaces:      []string{"custom-ns"},
				RestorePVs:             false,
				PreserveNodePorts:      false,
			},
			wantErr: false,
			check: func(restore *velerov1.Restore) error {
				expectedNamespaces := []string{"custom-ns"}
				if !reflect.DeepEqual(restore.Spec.IncludedNamespaces, expectedNamespaces) {
					return errorf("expected namespaces %v, got %v", expectedNamespaces, restore.Spec.IncludedNamespaces)
				}
				if restore.Spec.ExistingResourcePolicy != "none" {
					return errorf("expected existing resource policy 'none', got '%s'", restore.Spec.ExistingResourcePolicy)
				}
				if restore.Spec.RestorePVs == nil || *restore.Spec.RestorePVs {
					return errorf("expected RestorePVs to be false")
				}
				if restore.Spec.BackupName != "my-backup" {
					return errorf("expected backup name 'my-backup', got '%s'", restore.Spec.BackupName)
				}
				if restore.Spec.ScheduleName != "" {
					return errorf("expected empty schedule name, got '%s'", restore.Spec.ScheduleName)
				}
				return nil
			},
		},
		{
			name: "restore from schedule",
			opts: &CreateOptions{
				HCName:                 "sched-cluster",
				HCNamespace:            "clusters",
				ScheduleName:           "daily-backup",
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "update",
				RestorePVs:             true,
				PreserveNodePorts:      true,
			},
			wantErr: false,
			check: func(restore *velerov1.Restore) error {
				if restore.Spec.ScheduleName != "daily-backup" {
					return errorf("expected schedule name 'daily-backup', got '%s'", restore.Spec.ScheduleName)
				}
				if restore.Spec.BackupName != "" {
					return errorf("expected empty backup name, got '%s'", restore.Spec.BackupName)
				}
				expectedNamespaces := []string{"clusters", "clusters-sched-cluster"}
				if !reflect.DeepEqual(restore.Spec.IncludedNamespaces, expectedNamespaces) {
					return errorf("expected namespaces %v, got %v", expectedNamespaces, restore.Spec.IncludedNamespaces)
				}
				return nil
			},
		},
		{
			name: "restore with custom name",
			opts: &CreateOptions{
				HCName:                 "custom-cluster",
				HCNamespace:            "clusters",
				BackupName:             "my-backup",
				RestoreName:            "custom-restore-name",
				OADPNamespace:          "openshift-adp",
				ExistingResourcePolicy: "update",
				RestorePVs:             true,
				PreserveNodePorts:      true,
			},
			wantErr: false,
			check: func(restore *velerov1.Restore) error {
				if restore.Name != "custom-restore-name" {
					return errorf("expected restore name 'custom-restore-name', got '%s'", restore.Name)
				}
				if restore.Spec.BackupName != "my-backup" {
					return errorf("expected backup name 'my-backup', got '%s'", restore.Spec.BackupName)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore, restoreName, err := tt.opts.generateRestoreObject()
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOptions.generateRestoreObject() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			// Check basic properties
			if restore.Kind != "Restore" {
				t.Errorf("expected Kind 'Restore', got '%s'", restore.Kind)
			}
			if restore.APIVersion != "velero.io/v1" {
				t.Errorf("expected APIVersion 'velero.io/v1', got '%s'", restore.APIVersion)
			}
			if restore.Namespace != tt.opts.OADPNamespace {
				t.Errorf("expected namespace '%s', got '%s'", tt.opts.OADPNamespace, restore.Namespace)
			}
			if restoreName == "" {
				t.Error("restore name should not be empty")
			}
			if restore.Name != restoreName {
				t.Errorf("restore object name '%s' should match returned name '%s'", restore.Name, restoreName)
			}

			// Run custom checks
			if tt.check != nil {
				if err := tt.check(restore); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// Helper function to create error messages
func errorf(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

func TestDefaultExcludedResources(t *testing.T) {
	expectedResources := []string{
		"nodes",
		"events",
		"events.events.k8s.io",
		"backups.velero.io",
		"restores.velero.io",
		"resticrepositories.velero.io",
		"csinodes.storage.k8s.io",
		"volumeattachments.storage.k8s.io",
		"backuprepositories.velero.io",
	}

	if !reflect.DeepEqual(defaultExcludedResources, expectedResources) {
		t.Errorf("defaultExcludedResources = %v, want %v", defaultExcludedResources, expectedResources)
	}
}
