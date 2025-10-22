package restore

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/oadp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

type CreateOptions struct {
	// Required flags
	HCName       string
	HCNamespace  string
	BackupName   string
	ScheduleName string

	// Optional flags with defaults
	RestoreName            string
	OADPNamespace          string
	ExistingResourcePolicy string
	IncludeNamespaces      []string
	Render                 bool
	RestorePVs             bool
	PreserveNodePorts      bool

	// Client context
	Log    logr.Logger
	Client client.Client
}

var (
	// Default excluded resources for restore operations
	defaultExcludedResources = []string{
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
)

func NewCreateCommand() *cobra.Command {
	opts := &CreateOptions{
		Log: log.Log,
	}

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Create a restore operation from a hosted cluster backup",
		Long: `Create a restore operation from a hosted cluster backup using OADP (OpenShift API for Data Protection).

The restore command creates a Velero restore resource that will restore a previously backed up
hosted cluster. It automatically configures the appropriate namespaces and resource policies
for HyperShift hosted clusters.

Examples:
  # Restore from a specific backup
  hypershift create restore --hc-name production --hc-namespace clusters --from-backup prod-clusters-abc123

  # Restore from schedule (uses latest backup)
  hypershift create restore --hc-name production --hc-namespace clusters --from-schedule daily-backup

  # Restore with custom name
  hypershift create restore --hc-name production --hc-namespace clusters --from-backup prod-clusters-abc123 --name my-custom-restore

  # Restore to custom namespaces only
  hypershift create restore --hc-name production --hc-namespace clusters --from-backup prod-clusters-abc123 --include-namespaces custom-ns1,custom-ns2

  # Render restore YAML without creating it
  hypershift create restore --hc-name production --hc-namespace clusters --from-backup prod-clusters-abc123 --render

  # Non-destructive restore (don't update existing resources)
  hypershift create restore --hc-name production --hc-namespace clusters --from-backup prod-clusters-abc123 --existing-resource-policy none

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	// Required flags
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Name of the hosted cluster to restore (required)")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Namespace of the hosted cluster to restore (required)")
	cmd.Flags().StringVar(&opts.BackupName, "from-backup", "", "Name of the backup to restore from (mutually exclusive with --from-schedule)")
	cmd.Flags().StringVar(&opts.ScheduleName, "from-schedule", "", "Name of the schedule to restore from (uses latest backup, mutually exclusive with --from-backup)")

	// Optional flags with defaults
	cmd.Flags().StringVar(&opts.RestoreName, "name", "", "Custom name for the restore (auto-generated if not specified)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.ExistingResourcePolicy, "existing-resource-policy", "update", "Policy for handling existing resources (none, update)")
	cmd.Flags().StringSliceVar(&opts.IncludeNamespaces, "include-namespaces", nil, "Override included namespaces (by default includes hc-namespace and hc-namespace+hc-name)")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render the restore object to STDOUT instead of creating it")
	cmd.Flags().BoolVar(&opts.RestorePVs, "restore-pvs", true, "Restore persistent volumes")
	cmd.Flags().BoolVar(&opts.PreserveNodePorts, "preserve-node-ports", true, "Preserve NodePort assignments during restore")

	// Mark required flags - note that we'll validate backup OR schedule in Run()
	_ = cmd.MarkFlagRequired("hc-name")
	_ = cmd.MarkFlagRequired("hc-namespace")

	return cmd
}

func (o *CreateOptions) Run(ctx context.Context) error {
	// Validate that exactly one of backup or schedule is specified
	if err := o.validateBackupOrSchedule(); err != nil {
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
				restore, _, err := o.generateRestoreObject()
				if err != nil {
					return fmt.Errorf("restore generation failed: %w", err)
				}
				return oadp.RenderVeleroResource(restore)
			}
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	if o.Client != nil && !o.Render {
		// Step 1: Validate backup or schedule exists
		if o.BackupName != "" {
			o.Log.Info("Validating backup exists...")
			if err := o.validateBackupExists(ctx); err != nil {
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
			if err := o.validateBackupExists(ctx); err != nil {
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
	restore, restoreName, err := o.generateRestoreObject()
	if err != nil {
		return fmt.Errorf("restore generation failed: %w", err)
	}

	if o.Render {
		// Render mode: output YAML to STDOUT
		return oadp.RenderVeleroResource(restore)
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

func (o *CreateOptions) validateBackupExists(ctx context.Context) error {
	backup := &velerov1.Backup{}
	key := client.ObjectKey{
		Name:      o.BackupName,
		Namespace: o.OADPNamespace,
	}

	if err := o.Client.Get(ctx, key, backup); err != nil {
		return fmt.Errorf("backup '%s' not found in namespace '%s': %w", o.BackupName, o.OADPNamespace, err)
	}

	// Check if backup is completed
	if backup.Status.Phase != velerov1.BackupPhaseCompleted {
		return fmt.Errorf("backup '%s' is not completed, current phase: %s", o.BackupName, backup.Status.Phase)
	}

	return nil
}

func (o *CreateOptions) validateScheduleExists(ctx context.Context) error {
	schedule := &velerov1.Schedule{}
	key := client.ObjectKey{
		Name:      o.ScheduleName,
		Namespace: o.OADPNamespace,
	}

	if err := o.Client.Get(ctx, key, schedule); err != nil {
		return fmt.Errorf("schedule '%s' not found in namespace '%s': %w", o.ScheduleName, o.OADPNamespace, err)
	}

	// Check if schedule is enabled (optional check)
	if schedule.Spec.Paused {
		return fmt.Errorf("schedule '%s' is paused", o.ScheduleName)
	}

	return nil
}

// generateRestoreName creates a restore name using the format: {sourceName}-{hcName}-restore-{randomSuffix}
func generateRestoreName(sourceName, hcName string, randomGen func(int) string) string {
	randomSuffix := randomGen(6)
	return fmt.Sprintf("%s-%s-restore-%s", sourceName, hcName, randomSuffix)
}

func (o *CreateOptions) generateRestoreObject() (*velerov1.Restore, string, error) {
	var restoreName string

	// Use custom name if provided, otherwise generate one
	if o.RestoreName != "" {
		restoreName = o.RestoreName
	} else {
		// Determine source name for restore name generation
		sourceName := o.BackupName
		if o.ScheduleName != "" {
			sourceName = o.ScheduleName
		}
		// Generate restore name with hc-name included
		restoreName = generateRestoreName(sourceName, o.HCName, utilrand.String)
	}

	// Build included namespaces list
	includedNamespaces := o.buildIncludedNamespaces()

	// Create restore spec
	restoreSpec := velerov1.RestoreSpec{
		IncludedNamespaces:     includedNamespaces,
		ExcludedResources:      defaultExcludedResources,
		ExistingResourcePolicy: velerov1.PolicyType(o.ExistingResourcePolicy),
		RestorePVs:             &o.RestorePVs,
		PreserveNodePorts:      &o.PreserveNodePorts,
	}

	// Set either BackupName or ScheduleName, but not both
	if o.BackupName != "" {
		restoreSpec.BackupName = o.BackupName
	} else if o.ScheduleName != "" {
		restoreSpec.ScheduleName = o.ScheduleName
	}

	// Create restore object using Velero API
	restore := &velerov1.Restore{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Restore",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoreName,
			Namespace: o.OADPNamespace,
		},
		Spec: restoreSpec,
	}

	return restore, restoreName, nil
}

func (o *CreateOptions) buildIncludedNamespaces() []string {
	// If user specified custom namespaces, use those instead of defaults
	if len(o.IncludeNamespaces) > 0 {
		return o.IncludeNamespaces
	}

	// Otherwise use default namespaces for hosted cluster
	return []string{
		o.HCNamespace,
		fmt.Sprintf("%s-%s", o.HCNamespace, o.HCName),
	}
}
