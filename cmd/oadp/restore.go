package oadp

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/oadp"
	utilroute "github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

func NewCreateRestoreCommand() *cobra.Command {
	opts := &CreateOptions{
		Log: log.Log,
	}

	// CLI flag variables for boolean fields
	var restorePVs, preserveNodePorts bool

	cmd := &cobra.Command{
		Use:   "oadp-restore",
		Short: "Create a restore operation from a hosted cluster backup",
		Long: `Create a restore operation from a hosted cluster backup using OADP (OpenShift API for Data Protection).

The restore command creates a Velero restore resource that will restore a previously backed up
hosted cluster. It automatically configures the appropriate namespaces and resource policies
for HyperShift hosted clusters.

Examples:
  # Restore from a specific backup
  hypershift create oadp-restore --hc-name production --hc-namespace prod-cluster-ns --from-backup prod-clusters-abc123

  # Restore from schedule (uses latest backup)
  hypershift create oadp-restore --hc-name production --hc-namespace prod-cluster-ns --from-schedule daily-backup

  # Restore with custom name
  hypershift create oadp-restore --hc-name production --hc-namespace prod-cluster-ns --from-backup prod-clusters-abc123 --name my-custom-restore

  # Restore to custom namespaces only
  hypershift create oadp-restore --hc-name production --hc-namespace prod-cluster-ns --from-backup prod-clusters-abc123 --include-namespaces custom-ns1,custom-ns2

  # Render restore YAML without creating it
  hypershift create oadp-restore --hc-name production --hc-namespace prod-cluster-ns --from-backup prod-clusters-abc123 --render

  # Non-destructive restore (don't update existing resources)
  hypershift create oadp-restore --hc-name production --hc-namespace prod-cluster-ns --from-backup prod-clusters-abc123 --existing-resource-policy none

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Set pointer values from CLI flags
			opts.RestorePVs = &restorePVs
			opts.PreserveNodePorts = &preserveNodePorts
			return opts.RunRestore(cmd.Context())
		},
	}

	// Required flags
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Name of the hosted cluster to restore (required)")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Namespace of the hosted cluster to restore (required)")
	cmd.Flags().StringVar(&opts.BackupName, "from-backup", "", "Name of the backup to restore from (mutually exclusive with --from-schedule)")
	cmd.Flags().StringVar(&opts.ScheduleName, "from-schedule", "", "Name of the schedule to restore from (uses latest backup, mutually exclusive with --from-backup)")

	// Optional flags with defaults
	cmd.Flags().StringVar(&opts.RestoreName, "name", "", "Custom name for the restore (auto-generated if not provided)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.ExistingResourcePolicy, "existing-resource-policy", "update", "Policy for handling existing resources (none, update)")
	cmd.Flags().StringSliceVar(&opts.IncludeNamespaces, "include-additional-namespaces", nil, "Additional namespaces to include (HC and HCP namespaces are always included)")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render the restore object to STDOUT instead of creating it")
	cmd.Flags().BoolVar(&restorePVs, "restore-pvs", true, "Restore persistent volumes")
	cmd.Flags().BoolVar(&preserveNodePorts, "preserve-node-ports", true, "Preserve NodePort assignments during restore")

	// Mark required flags - note that we'll validate backup OR schedule in Run()
	_ = cmd.MarkFlagRequired("hc-name")
	_ = cmd.MarkFlagRequired("hc-namespace")

	return cmd
}

// GenerateRestoreName creates a restore name using the format: {hcName}-{hcNamespace}-{randomSuffix}
// If the name is too long, it uses utils.ShortenName to ensure it doesn't exceed 63 characters
func GenerateRestoreName(hcName, hcNamespace string) string {
	randomSuffix := utilrand.String(6)
	baseName := fmt.Sprintf("%s-%s", hcName, hcNamespace)
	// Use ShortenName to ensure it doesn't exceed DNS1123 subdomain max length (63 chars)
	return utilroute.ShortenName(baseName, randomSuffix, validation.DNS1123LabelMaxLength)
}

func (o *CreateOptions) RunRestore(ctx context.Context) error {
	// Validate that exactly one of backup or schedule is specified
	if err := o.validateBackupOrSchedule(); err != nil {
		return err
	}

	// Validate restore name if provided
	if err := o.ValidateRestoreName(); err != nil {
		return err
	}

	// Validate existing resource policy
	if err := o.validateExistingResourcePolicy(); err != nil {
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
				restore, _, err := o.GenerateRestoreObject()
				if err != nil {
					return fmt.Errorf("restore generation failed: %w", err)
				}
				err = renderYAMLObject(restore)
				if err != nil {
					return err
				}
				return nil
			}
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	if o.Client != nil && !o.Render {
		// Step 1: Validate backup or schedule exists
		if o.BackupName != "" {
			o.Log.Info("Validating backup exists...")
			if err := o.validateBackupExists(ctx, false); err != nil {
				return fmt.Errorf("backup validation failed: %w", err)
			}
		} else if o.ScheduleName != "" {
			o.Log.Info("Validating schedule exists...")
			if err := o.validateScheduleExists(ctx); err != nil {
				return fmt.Errorf("schedule validation failed: %w", err)
			}
		}

		// Step 2: Validate OADP installation
		o.Log.Info("Validating OADP installation...")
		if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
			return fmt.Errorf("OADP validation failed: %w", err)
		}

		// Step 3: Verify DPA CR exists
		o.Log.Info("Verifying DataProtectionApplication resource...")
		if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
			return fmt.Errorf("DPA verification failed: %w", err)
		}
	} else if o.Client != nil && o.Render {
		// In render mode, run optional validations
		if o.BackupName != "" {
			o.Log.Info("Validating backup exists...")
			if err := o.validateBackupExists(ctx, true); err != nil {
				o.Log.Info("Warning: Backup validation failed, but continuing with render", "error", err.Error())
			}
		} else if o.ScheduleName != "" {
			o.Log.Info("Validating schedule exists...")
			if err := o.validateScheduleExists(ctx); err != nil {
				o.Log.Info("Warning: Schedule validation failed, but continuing with render", "error", err.Error())
			}
		}

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

	// Step 3: Generate restore object
	restore, restoreName, err := o.GenerateRestoreObject()
	if err != nil {
		return fmt.Errorf("restore generation failed: %w", err)
	}

	if o.Render {
		// Render mode: output YAML to STDOUT
		err := renderYAMLObject(restore)
		if err != nil {
			return err
		}
		return nil
	} else {
		// Normal mode: create the restore
		o.Log.Info("Creating restore...")
		if err := o.Client.Create(ctx, restore); err != nil {
			return fmt.Errorf("failed to create restore resource: %w", err)
		}
		if o.BackupName != "" {
			o.Log.Info("Restore created successfully", "name", restoreName, "namespace", o.OADPNamespace, "backup", o.BackupName)
		} else {
			o.Log.Info("Restore created successfully", "name", restoreName, "namespace", o.OADPNamespace, "schedule", o.ScheduleName)
		}
	}

	return nil
}

// ValidateRestoreName validates the custom restore name if provided
func (o *CreateOptions) ValidateRestoreName() error {
	if o.RestoreName != "" {
		// Kubernetes resource names must be 63 characters or less
		if len(o.RestoreName) > 63 {
			return fmt.Errorf("restore name '%s' is too long (%d characters). Kubernetes resource names must be 63 characters or less. Use --name to specify a shorter custom name", o.RestoreName, len(o.RestoreName))
		}
		// Use Kubernetes official DNS subdomain validation
		if errs := validation.IsDNS1123Subdomain(o.RestoreName); len(errs) > 0 {
			return fmt.Errorf("restore name '%s' is invalid: %s", o.RestoreName, strings.Join(errs, "; "))
		}
	}
	return nil
}

func (o *CreateOptions) validateBackupOrSchedule() error {
	hasBackup := o.BackupName != ""
	hasSchedule := o.ScheduleName != ""

	if !hasBackup && !hasSchedule {
		return fmt.Errorf("either --from-backup or --from-schedule must be specified")
	}

	if hasBackup && hasSchedule {
		return fmt.Errorf("--from-backup and --from-schedule are mutually exclusive, specify only one")
	}

	return nil
}

func (o *CreateOptions) validateExistingResourcePolicy() error {
	validPolicies := []string{"none", "update"}
	for _, policy := range validPolicies {
		if o.ExistingResourcePolicy == policy {
			return nil
		}
	}
	return fmt.Errorf("invalid existing-resource-policy '%s'. Valid values are: %s", o.ExistingResourcePolicy, strings.Join(validPolicies, ", "))
}

func (o *CreateOptions) validateBackupExists(ctx context.Context, renderMode bool) error {
	backup := &unstructured.Unstructured{}
	backup.SetAPIVersion("velero.io/v1")
	backup.SetKind("Backup")

	key := client.ObjectKey{
		Name:      o.BackupName,
		Namespace: o.OADPNamespace,
	}

	if err := o.Client.Get(ctx, key, backup); err != nil {
		return fmt.Errorf("backup '%s' not found in namespace '%s': %w", o.BackupName, o.OADPNamespace, err)
	}

	// Check if backup is completed
	phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get backup phase: %w", err)
	}
	if found && phase != "Completed" {
		if renderMode {
			o.Log.Info("Warning: Backup is not completed, but proceeding with render", "backup", o.BackupName, "phase", phase)
		} else {
			return fmt.Errorf("backup '%s' is not completed, current phase: %s", o.BackupName, phase)
		}
	}

	return nil
}

func (o *CreateOptions) validateScheduleExists(ctx context.Context) error {
	schedule := &unstructured.Unstructured{}
	schedule.SetAPIVersion("velero.io/v1")
	schedule.SetKind("Schedule")

	key := client.ObjectKey{
		Name:      o.ScheduleName,
		Namespace: o.OADPNamespace,
	}

	if err := o.Client.Get(ctx, key, schedule); err != nil {
		return fmt.Errorf("schedule '%s' not found in namespace '%s': %w", o.ScheduleName, o.OADPNamespace, err)
	}

	// Check if schedule is enabled (optional check)
	paused, found, err := unstructured.NestedBool(schedule.Object, "spec", "paused")
	if err != nil {
		return fmt.Errorf("failed to get schedule paused status: %w", err)
	}
	if found && paused {
		return fmt.Errorf("schedule '%s' is paused", o.ScheduleName)
	}

	return nil
}

func (o *CreateOptions) GenerateRestoreObject() (*unstructured.Unstructured, string, error) {
	// Apply default values if not set
	if o.ExistingResourcePolicy == "" {
		o.ExistingResourcePolicy = "update"
	}
	if o.OADPNamespace == "" {
		o.OADPNamespace = "openshift-adp"
	}
	// Apply CLI default values for boolean fields when not explicitly set
	// The CLI defaults are true for both restore-pvs and preserve-node-ports
	if o.RestorePVs == nil {
		defaultRestorePVs := true
		o.RestorePVs = &defaultRestorePVs
	}
	if o.PreserveNodePorts == nil {
		defaultPreserveNodePorts := true
		o.PreserveNodePorts = &defaultPreserveNodePorts
	}

	// Use the name from flag, or generate if empty
	restoreName := o.RestoreName
	if restoreName == "" {
		restoreName = GenerateRestoreName(o.HCName, o.HCNamespace)
	}

	// Build included namespaces list
	includedNamespaces := buildIncludedNamespaces(o.HCNamespace, o.HCName, o.IncludeNamespaces)

	// Convert string slices to interface slices for unstructured objects
	includedNamespacesInterface := make([]interface{}, len(includedNamespaces))
	for i, ns := range includedNamespaces {
		includedNamespacesInterface[i] = ns
	}

	excludedResourcesInterface := make([]interface{}, len(defaultExcludedResources))
	for i, res := range defaultExcludedResources {
		excludedResourcesInterface[i] = res
	}

	// Create restore spec
	spec := map[string]interface{}{
		"includedNamespaces":     includedNamespacesInterface,
		"excludedResources":      excludedResourcesInterface,
		"existingResourcePolicy": o.ExistingResourcePolicy,
		"restorePVs":             *o.RestorePVs,
		"preserveNodePorts":      *o.PreserveNodePorts,
	}

	// Set either BackupName or ScheduleName, but not both
	if o.BackupName != "" {
		spec["backupName"] = o.BackupName
	} else if o.ScheduleName != "" {
		spec["scheduleName"] = o.ScheduleName
	}

	// Create restore object using unstructured
	restore := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind":       "Restore",
			"metadata": map[string]interface{}{
				"name":      restoreName,
				"namespace": o.OADPNamespace,
			},
			"spec": spec,
		},
	}

	return restore, restoreName, nil
}
