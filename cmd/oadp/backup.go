package oadp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/oadp"
	utilroute "github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/spf13/cobra"
)

// Note: CreateOptions is now defined in types.go

// Note: All variable declarations are now in types.go

func NewCreateBackupCommand() *cobra.Command {
	opts := &CreateOptions{
		Log: log.Log,
	}

	cmd := &cobra.Command{
		Use:   "oadp-backup",
		Short: "Create a backup of a hosted cluster using OADP",
		Long: `Create a backup of a hosted cluster using OADP (OpenShift API for Data Protection).

The oadp-backup command automatically detects the platform type of your HostedCluster and includes
the appropriate platform-specific resources. It validates OADP installation and creates
comprehensive backups that can be used for disaster recovery scenarios.

Examples:
  # Basic backup with default settings
  hypershift create oadp-backup --hc-name prod-cluster --hc-namespace prod-cluster-ns

  # Backup with custom storage location and TTL
  hypershift create oadp-backup --hc-name dev-cluster --hc-namespace dev-cluster-ns --storage-location s3-backup --ttl 24h

  # Render backup YAML without creating it (for GitOps workflows)
  hypershift create oadp-backup --hc-name test-cluster --hc-namespace test-cluster-ns --render

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.RunBackup(cmd.Context())
		},
	}

	// Required flags
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Name of the hosted cluster to backup (required)")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Namespace of the hosted cluster to backup (required)")

	// Optional flags with defaults
	cmd.Flags().StringVar(&opts.BackupCustomName, "name", "", "Custom name for the backup (auto-generated if not provided)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.StorageLocation, "storage-location", "default", "Storage location for the backup")
	cmd.Flags().DurationVar(&opts.TTL, "ttl", 2*time.Hour, "Time to live for the backup")
	cmd.Flags().BoolVar(&opts.SnapshotMoveData, "snapshot-move-data", true, "Enable snapshot move data feature")
	cmd.Flags().BoolVar(&opts.DefaultVolumesToFsBackup, "default-volumes-to-fs-backup", false, "Use filesystem backup for volumes by default")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render the backup object to STDOUT instead of creating it")
	cmd.Flags().StringSliceVar(&opts.IncludedResources, "included-resources", nil, "Comma-separated list of resources to include in backup (overrides defaults)")
	cmd.Flags().StringSliceVar(&opts.IncludeNamespaces, "include-additional-namespaces", nil, "Additional namespaces to include (HC and HCP namespaces are always included)")

	// Mark required flags
	_ = cmd.MarkFlagRequired("hc-name")
	_ = cmd.MarkFlagRequired("hc-namespace")

	return cmd
}

func (o *CreateOptions) RunBackup(ctx context.Context) error {
	// Validate backup name length
	if err := o.ValidateBackupName(); err != nil {
		return err
	}

	// Client is needed for validations and actual creation
	if o.Client == nil {
		var err error
		o.Client, err = util.GetClient()
		if err != nil {
			if o.Render {
				// In render mode, if we can't connect to cluster, we'll still render but skip validations
				o.Log.Info("Warning: Cannot connect to cluster for validation, skipping all checks")
				// Step: Generate backup object with default platform (AWS)
				backup, _, err := o.GenerateBackupObjectWithPlatform("AWS")
				if err != nil {
					return fmt.Errorf("backup generation failed: %w", err)
				}
				return renderYAMLObject(backup)
			}
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	var platform string
	if o.Client != nil {
		// Step 1: Validate HostedCluster exists and get platform
		o.Log.Info("Validating HostedCluster...")
		detectedPlatform, err := oadp.ValidateAndGetHostedClusterPlatform(ctx, o.Client, o.HCName, o.HCNamespace)
		if err != nil {
			if o.Render {
				o.Log.Info("Warning: HostedCluster validation failed, using default platform (AWS)", "error", err.Error())
				platform = "AWS"
			} else {
				return fmt.Errorf("HostedCluster validation failed: %w", err)
			}
		} else {
			platform = detectedPlatform
		}

		if !o.Render {
			// Step 2: Validate OADP components (only in non-render mode)
			o.Log.Info("Validating OADP installation...")
			if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
				return fmt.Errorf("OADP validation failed: %w", err)
			}

			// Step 3: Verify DPA CR exists (only in non-render mode)
			o.Log.Info("Verifying DataProtectionApplication resource...")
			if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
				return fmt.Errorf("DPA verification failed: %w", err)
			}
		} else {
			// In render mode, run optional validations
			o.Log.Info("Validating OADP installation...")
			if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
				o.Log.Info("Warning: OADP validation failed, but continuing with render", "error", err.Error())
			} else {
				o.Log.Info("Verifying DataProtectionApplication resource...")
				if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
					o.Log.Info("Warning: DPA verification failed, but continuing with render", "error", err.Error())
				}
			}
		}
	} else {
		// This shouldn't happen but just in case
		platform = "AWS"
	}

	// Step 4: Generate backup object with detected platform
	backup, backupName, err := o.GenerateBackupObjectWithPlatform(platform)
	if err != nil {
		return fmt.Errorf("backup generation failed: %w", err)
	}

	if o.Render {
		// Render mode: output YAML to STDOUT
		err := renderYAMLObject(backup)
		if err != nil {
			return err
		}
		// Ensure proper newline after render for terminal prompt
		fmt.Fprintln(os.Stderr, "")
		return nil
	} else {
		// Normal mode: create the backup
		// Validate that client is available for creation
		if o.Client == nil {
			return fmt.Errorf("kubernetes client is required for resource creation (not in render mode)")
		}

		o.Log.Info("Creating backup...")
		if err := o.Client.Create(ctx, backup); err != nil {
			return fmt.Errorf("failed to create backup resource: %w", err)
		}
		o.Log.Info("Backup created successfully", "name", backupName, "namespace", o.OADPNamespace, "platform", platform)
	}

	return nil
}

// Note: randomStringGenerator type is now in types.go

// GenerateBackupName creates a backup name using the format: {hcName}-{hcNamespace}-{randomSuffix}
// If the name is too long, it uses utils.ShortenName to ensure it doesn't exceed 63 characters
func GenerateBackupName(hcName, hcNamespace string) string {
	randomSuffix := utilrand.String(6)
	baseName := fmt.Sprintf("%s-%s", hcName, hcNamespace)
	// Use ShortenName to ensure it doesn't exceed DNS1123 subdomain max length (63 chars)
	return utilroute.ShortenName(baseName, randomSuffix, validation.DNS1123LabelMaxLength)
}

// ValidateBackupName validates the custom backup name if provided
func (o *CreateOptions) ValidateBackupName() error {
	if o.BackupCustomName != "" {
		// Kubernetes resource names must be 63 characters or less
		if len(o.BackupCustomName) > 63 {
			return fmt.Errorf("backup name '%s' is too long (%d characters). Kubernetes resource names must be 63 characters or less. Use --name to specify a shorter custom name", o.BackupCustomName, len(o.BackupCustomName))
		}
		// Use Kubernetes official DNS subdomain validation
		if errs := validation.IsDNS1123Subdomain(o.BackupCustomName); len(errs) > 0 {
			return fmt.Errorf("backup name '%s' is invalid: %s", o.BackupCustomName, strings.Join(errs, "; "))
		}
	}
	return nil
}

func (o *CreateOptions) GenerateBackupObjectWithPlatform(platform string) (*unstructured.Unstructured, string, error) {
	// Apply default values if not set
	if o.StorageLocation == "" {
		o.StorageLocation = "default"
	}
	if o.TTL == 0 {
		o.TTL = 2 * time.Hour
	}
	if o.OADPNamespace == "" {
		o.OADPNamespace = "openshift-adp"
	}

	// Use the name from flag, or generate if empty
	backupName := o.BackupCustomName
	if backupName == "" {
		backupName = GenerateBackupName(o.HCName, o.HCNamespace)
	}

	var includedResources []string
	if o.IncludedResources != nil {
		includedResources = o.IncludedResources
	} else {
		// Use default resources based on platform
		includedResources = getDefaultResourcesForPlatform(platform)
	}

	// Create backup object using unstructured to avoid Velero dependency
	backup := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata": map[string]interface{}{
				"name":      backupName,
				"namespace": o.OADPNamespace,
				"labels": map[string]interface{}{
					"velero.io/storage-location": o.StorageLocation,
				},
			},
			"spec": map[string]interface{}{
				"includedNamespaces":       buildIncludedNamespaces(o.HCNamespace, o.HCName, o.IncludeNamespaces),
				"includedResources":        includedResources,
				"storageLocation":          o.StorageLocation,
				"ttl":                      o.TTL.String(),
				"snapshotMoveData":         o.SnapshotMoveData,
				"defaultVolumesToFsBackup": o.DefaultVolumesToFsBackup,
				"dataMover":                "velero",
				"snapshotVolumes":          true,
			},
		},
	}
	return backup, backupName, nil
}
