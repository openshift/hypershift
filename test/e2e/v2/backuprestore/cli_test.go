//go:build e2ev2 && backuprestore

package backuprestore

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBuildBackupArgs(t *testing.T) {
	tests := []struct {
		name     string
		opts     *OADPBackupOptions
		expected []string
	}{
		{
			name: "minimal required fields",
			opts: &OADPBackupOptions{
				HCName:      "my-cluster",
				HCNamespace: "clusters",
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
			},
		},
		{
			name: "all optional string fields",
			opts: &OADPBackupOptions{
				HCName:          "my-cluster",
				HCNamespace:     "clusters",
				Name:            "custom-backup",
				OADPNamespace:   "custom-oadp",
				StorageLocation: "aws-backup",
				TTL:             "24h",
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--name", "custom-backup",
				"--oadp-namespace", "custom-oadp",
				"--storage-location", "aws-backup",
				"--ttl", "24h",
			},
		},
		{
			name: "snapshot-move-data true",
			opts: &OADPBackupOptions{
				HCName:           "my-cluster",
				HCNamespace:      "clusters",
				SnapshotMoveData: boolPtr(true),
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--snapshot-move-data=true",
			},
		},
		{
			name: "snapshot-move-data false",
			opts: &OADPBackupOptions{
				HCName:           "my-cluster",
				HCNamespace:      "clusters",
				SnapshotMoveData: boolPtr(false),
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--snapshot-move-data=false",
			},
		},
		{
			name: "boolean flags",
			opts: &OADPBackupOptions{
				HCName:                   "my-cluster",
				HCNamespace:              "clusters",
				DefaultVolumesToFsBackup: true,
				Render:                   true,
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--default-volumes-to-fs-backup=true",
				"--render",
			},
		},
		{
			name: "slice fields",
			opts: &OADPBackupOptions{
				HCName:            "my-cluster",
				HCNamespace:       "clusters",
				IncludedResources: []string{"pods", "deployments", "services"},
				IncludeNamespaces: []string{"ns1", "ns2"},
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--included-resources", "pods,deployments,services",
				"--include-additional-namespaces", "ns1,ns2",
			},
		},
		{
			name: "all fields combined",
			opts: &OADPBackupOptions{
				HCName:                   "my-cluster",
				HCNamespace:              "clusters",
				Name:                     "full-backup",
				OADPNamespace:            "custom-oadp",
				StorageLocation:          "aws-backup",
				TTL:                      "48h",
				SnapshotMoveData:         boolPtr(true),
				DefaultVolumesToFsBackup: true,
				Render:                   true,
				IncludedResources:        []string{"pods"},
				IncludeNamespaces:        []string{"ns1"},
			},
			expected: []string{
				"create", "oadp-backup",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--name", "full-backup",
				"--oadp-namespace", "custom-oadp",
				"--storage-location", "aws-backup",
				"--ttl", "48h",
				"--snapshot-move-data=true",
				"--default-volumes-to-fs-backup=true",
				"--render",
				"--included-resources", "pods",
				"--include-additional-namespaces", "ns1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildBackupArgs(tt.opts)
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("buildBackupArgs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildRestoreArgs(t *testing.T) {
	tests := []struct {
		name     string
		opts     *OADPRestoreOptions
		expected []string
	}{
		{
			name: "minimal with from-backup",
			opts: &OADPRestoreOptions{
				HCName:      "my-cluster",
				HCNamespace: "clusters",
				FromBackup:  "backup-123",
			},
			expected: []string{
				"create", "oadp-restore",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--from-backup", "backup-123",
			},
		},
		{
			name: "with from-schedule",
			opts: &OADPRestoreOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				FromSchedule: "daily-backup",
			},
			expected: []string{
				"create", "oadp-restore",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--from-schedule", "daily-backup",
			},
		},
		{
			name: "all optional fields",
			opts: &OADPRestoreOptions{
				HCName:                 "my-cluster",
				HCNamespace:            "clusters",
				FromBackup:             "backup-123",
				Name:                   "custom-restore",
				OADPNamespace:          "custom-oadp",
				ExistingResourcePolicy: "update",
				RestorePVs:             boolPtr(true),
				PreserveNodePorts:      boolPtr(false),
				Render:                 true,
				IncludeNamespaces:      []string{"ns1", "ns2"},
			},
			expected: []string{
				"create", "oadp-restore",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--from-backup", "backup-123",
				"--name", "custom-restore",
				"--oadp-namespace", "custom-oadp",
				"--existing-resource-policy", "update",
				"--restore-pvs=true",
				"--preserve-node-ports=false",
				"--render",
				"--include-additional-namespaces", "ns1,ns2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildRestoreArgs(tt.opts)
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("buildRestoreArgs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildScheduleArgs(t *testing.T) {
	tests := []struct {
		name     string
		opts     *OADPScheduleOptions
		expected []string
	}{
		{
			name: "minimal required fields",
			opts: &OADPScheduleOptions{
				HCName:      "my-cluster",
				HCNamespace: "clusters",
				Schedule:    "0 1 * * *",
			},
			expected: []string{
				"create", "oadp-schedule",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--schedule", "0 1 * * *",
			},
		},
		{
			name: "all optional fields",
			opts: &OADPScheduleOptions{
				HCName:                   "my-cluster",
				HCNamespace:              "clusters",
				Schedule:                 "daily",
				Name:                     "custom-schedule",
				OADPNamespace:            "custom-oadp",
				StorageLocation:          "aws-backup",
				TTL:                      "720h",
				SnapshotMoveData:         boolPtr(false),
				DefaultVolumesToFsBackup: true,
				Render:                   true,
				Paused:                   true,
				UseOwnerReferences:       true,
				SkipImmediately:          true,
				IncludedResources:        []string{"pods", "services"},
				IncludeNamespaces:        []string{"ns1"},
			},
			expected: []string{
				"create", "oadp-schedule",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--schedule", "daily",
				"--name", "custom-schedule",
				"--oadp-namespace", "custom-oadp",
				"--storage-location", "aws-backup",
				"--ttl", "720h",
				"--snapshot-move-data=false",
				"--default-volumes-to-fs-backup=true",
				"--render",
				"--paused",
				"--use-owner-references",
				"--skip-immediately",
				"--included-resources", "pods,services",
				"--include-additional-namespaces", "ns1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildScheduleArgs(tt.opts)
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("buildScheduleArgs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// boolPtr is a helper function to create a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}
