//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
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

func TestBuildFixDrOidcIamArgs(t *testing.T) {
	tests := []struct {
		name     string
		opts     *FixDrOidcIamOptions
		expected []string
	}{
		{
			name: "hosted cluster mode with aws creds",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				AWSCredsFile: "/path/to/creds",
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--aws-creds", "/path/to/creds",
			},
		},
		{
			name: "hosted cluster mode with sts creds",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				STSCredsFile: "/path/to/sts-creds",
				RoleARN:      "arn:aws:iam::123456789012:role/my-role",
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--sts-creds", "/path/to/sts-creds",
				"--role-arn", "arn:aws:iam::123456789012:role/my-role",
			},
		},
		{
			name: "manual mode with aws creds",
			opts: &FixDrOidcIamOptions{
				InfraID:      "my-infra-123",
				Region:       "us-east-1",
				AWSCredsFile: "/path/to/creds",
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--infra-id", "my-infra-123",
				"--region", "us-east-1",
				"--aws-creds", "/path/to/creds",
			},
		},
		{
			name: "manual mode with sts creds",
			opts: &FixDrOidcIamOptions{
				InfraID:      "my-infra-123",
				Region:       "us-west-2",
				STSCredsFile: "/path/to/sts-creds",
				RoleARN:      "arn:aws:iam::123456789012:role/my-role",
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--infra-id", "my-infra-123",
				"--region", "us-west-2",
				"--sts-creds", "/path/to/sts-creds",
				"--role-arn", "arn:aws:iam::123456789012:role/my-role",
			},
		},
		{
			name: "hosted cluster mode with oidc bucket and issuer",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				OIDCBucket:   "my-oidc-bucket",
				Issuer:       "https://my-issuer.example.com",
				AWSCredsFile: "/path/to/creds",
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--oidc-bucket", "my-oidc-bucket",
				"--issuer", "https://my-issuer.example.com",
				"--aws-creds", "/path/to/creds",
			},
		},
		{
			name: "hosted cluster mode with all optional flags",
			opts: &FixDrOidcIamOptions{
				HCName:        "my-cluster",
				HCNamespace:   "clusters",
				OIDCBucket:    "my-oidc-bucket",
				Issuer:        "https://issuer.example.com",
				AWSCredsFile:  "/path/to/creds",
				Timeout:       10 * time.Minute,
				DryRun:        true,
				ForceRecreate: true,
				RestartDelay:  1 * time.Minute,
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--hc-name", "my-cluster",
				"--hc-namespace", "clusters",
				"--oidc-bucket", "my-oidc-bucket",
				"--issuer", "https://issuer.example.com",
				"--aws-creds", "/path/to/creds",
				"--timeout", "10m0s",
				"--dry-run",
				"--force-recreate",
				"--restart-delay", "1m0s",
			},
		},
		{
			name: "manual mode with all optional flags",
			opts: &FixDrOidcIamOptions{
				InfraID:       "my-infra-123",
				Region:        "us-west-2",
				OIDCBucket:    "my-oidc-bucket",
				Issuer:        "https://issuer.example.com",
				AWSCredsFile:  "/path/to/creds",
				Timeout:       5 * time.Minute,
				DryRun:        true,
				ForceRecreate: true,
				RestartDelay:  30 * time.Second,
			},
			expected: []string{
				"fix", "dr-oidc-iam",
				"--infra-id", "my-infra-123",
				"--region", "us-west-2",
				"--oidc-bucket", "my-oidc-bucket",
				"--issuer", "https://issuer.example.com",
				"--aws-creds", "/path/to/creds",
				"--timeout", "5m0s",
				"--dry-run",
				"--force-recreate",
				"--restart-delay", "30s",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildFixDrOidcIamArgs(tt.opts)
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("buildFixDrOidcIamArgs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunFixDrOidcIamValidation(t *testing.T) {
	tests := []struct {
		name        string
		opts        *FixDrOidcIamOptions
		expectedErr string
	}{
		{
			name: "hc-name without hc-namespace",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				AWSCredsFile: "/path/to/creds",
			},
			expectedErr: "--hc-namespace is required when using --hc-name",
		},
		{
			name: "hc-namespace without hc-name",
			opts: &FixDrOidcIamOptions{
				HCNamespace:  "clusters",
				AWSCredsFile: "/path/to/creds",
			},
			expectedErr: "--hc-namespace can only be used with --hc-name",
		},
		{
			name: "both modes specified",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				InfraID:      "my-infra",
				Region:       "us-east-1",
				AWSCredsFile: "/path/to/creds",
			},
			expectedErr: "when using --hc-name, --infra-id and --region should not be specified",
		},
		{
			name: "manual mode missing infra-id",
			opts: &FixDrOidcIamOptions{
				Region:       "us-east-1",
				AWSCredsFile: "/path/to/creds",
			},
			expectedErr: "--infra-id and --region are required when --hc-name is not set",
		},
		{
			name: "manual mode missing region",
			opts: &FixDrOidcIamOptions{
				InfraID:      "my-infra",
				AWSCredsFile: "/path/to/creds",
			},
			expectedErr: "--infra-id and --region are required when --hc-name is not set",
		},
		{
			name: "both credential modes specified",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				AWSCredsFile: "/path/to/creds",
				STSCredsFile: "/path/to/sts-creds",
				RoleARN:      "arn:aws:iam::123456789012:role/my-role",
			},
			expectedErr: "only one of 'aws-creds' or 'sts-creds'/'role-arn' can be provided",
		},
		{
			name: "no credentials",
			opts: &FixDrOidcIamOptions{
				HCName:      "my-cluster",
				HCNamespace: "clusters",
			},
			expectedErr: "either 'aws-creds' or both 'sts-creds' and 'role-arn' must be provided",
		},
		{
			name: "sts-creds without role-arn",
			opts: &FixDrOidcIamOptions{
				HCName:       "my-cluster",
				HCNamespace:  "clusters",
				STSCredsFile: "/path/to/sts-creds",
			},
			expectedErr: "role-arn is required when using sts-creds",
		},
		{
			name: "role-arn without sts-creds",
			opts: &FixDrOidcIamOptions{
				HCName:      "my-cluster",
				HCNamespace: "clusters",
				RoleARN:     "arn:aws:iam::123456789012:role/my-role",
			},
			expectedErr: "sts-creds is required when using role-arn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// RunFixDrOidcIam will fail at validation before reaching CLI lookup
			err := RunFixDrOidcIam(context.Background(), logr.Discard(), "/tmp/artifacts", tt.opts)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.expectedErr)
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("expected error containing %q, got %q", tt.expectedErr, err.Error())
			}
		})
	}
}

// boolPtr is a helper function to create a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}
