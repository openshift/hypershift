package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/oadp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

type CreateOptions struct {
	// Required flags
	HCName      string
	HCNamespace string
	Schedule    string

	// Optional flags with defaults
	ScheduleName             string
	OADPNamespace            string
	StorageLocation          string
	TTL                      time.Duration
	SnapshotMoveData         bool
	DefaultVolumesToFsBackup bool
	Render                   bool
	IncludedResources        []string
	Paused                   bool
	UseOwnerReferences       bool
	SkipImmediately          bool

	// Client context
	Log    logr.Logger
	Client client.Client
}

var (
	// Common cron schedule presets for user convenience
	schedulePresets = map[string]string{
		"daily":   "0 2 * * *",    // Daily at 2 AM
		"weekly":  "0 2 * * 0",    // Weekly on Sunday at 2 AM
		"monthly": "0 2 1 * *",    // Monthly on the 1st at 2 AM
		"hourly":  "0 * * * *",    // Every hour at minute 0
	}
)

func NewCreateCommand() *cobra.Command {
	opts := &CreateOptions{
		Log: log.Log,
	}

	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Create a backup schedule for a hosted cluster",
		Long: `Create a backup schedule for a hosted cluster using OADP (OpenShift API for Data Protection).

The schedule command automatically detects the platform type of your HostedCluster and includes
the appropriate platform-specific resources. It validates OADP installation and creates
recurring backup schedules using standard cron syntax or convenient presets.

Schedule Presets:
  daily   - 0 2 * * *   (Every day at 2 AM)
  weekly  - 0 2 * * 0   (Every Sunday at 2 AM)
  monthly - 0 2 1 * *   (Every 1st of month at 2 AM)
  hourly  - 0 * * * *   (Every hour at minute 0)

Examples:
  # Daily backup schedule with defaults
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule daily

  # Custom cron schedule (every 6 hours)
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule "0 */6 * * *"

  # Weekly schedule with custom settings
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule weekly --storage-location s3-backup --ttl 168h

  # Schedule with custom resource selection
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule daily --included-resources hostedcluster,nodepool,secrets,configmap

  # Render schedule YAML without creating it
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule daily --render

  # Create paused schedule (can be enabled later)
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule daily --paused

  # Monthly schedule with data movement for cross-region backups
  hypershift create schedule --hc-name production --hc-namespace clusters --schedule monthly --snapshot-move-data=true --ttl 2160h

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	// Required flags
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Name of the hosted cluster to schedule backups for (required)")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Namespace of the hosted cluster to schedule backups for (required)")
	cmd.Flags().StringVar(&opts.Schedule, "schedule", "", "Cron schedule or preset (daily, weekly, monthly, hourly) for backups (required)")

	// Optional flags with defaults
	cmd.Flags().StringVar(&opts.ScheduleName, "name", "", "Custom name for the schedule (auto-generated if not specified)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.StorageLocation, "storage-location", "default", "Storage location for the backups")
	cmd.Flags().DurationVar(&opts.TTL, "ttl", 2*time.Hour, "Time to live for individual backups created by this schedule")
	cmd.Flags().BoolVar(&opts.SnapshotMoveData, "snapshot-move-data", true, "Enable snapshot move data feature")
	cmd.Flags().BoolVar(&opts.DefaultVolumesToFsBackup, "default-volumes-to-fs-backup", false, "Use filesystem backup for volumes by default")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render the schedule object to STDOUT instead of creating it")
	cmd.Flags().StringSliceVar(&opts.IncludedResources, "included-resources", nil, "Comma-separated list of resources to include in backup (overrides defaults)")
	cmd.Flags().BoolVar(&opts.Paused, "paused", false, "Create schedule in paused state")
	cmd.Flags().BoolVar(&opts.UseOwnerReferences, "use-owner-references", false, "Use owner references in backup objects")
	cmd.Flags().BoolVar(&opts.SkipImmediately, "skip-immediately", false, "Skip backup immediately if schedule is due when created")

	// Mark required flags
	_ = cmd.MarkFlagRequired("hc-name")
	_ = cmd.MarkFlagRequired("hc-namespace")
	_ = cmd.MarkFlagRequired("schedule")

	return cmd
}

func (o *CreateOptions) Run(ctx context.Context) error {
	// Validate and resolve schedule
	if err := o.validateAndResolveSchedule(); err != nil {
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
				// Step: Generate schedule object with default platform (AWS)
				schedule, _, err := o.generateScheduleObjectWithPlatform("AWS")
				if err != nil {
					return fmt.Errorf("schedule generation failed: %w", err)
				}
				return oadp.RenderVeleroResource(schedule)
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

	// Step 4: Generate schedule object with detected platform
	schedule, scheduleName, err := o.generateScheduleObjectWithPlatform(platform)
	if err != nil {
		return fmt.Errorf("schedule generation failed: %w", err)
	}

	if o.Render {
		// Render mode: output YAML to STDOUT
		return oadp.RenderVeleroResource(schedule)
	} else {
		// Normal mode: create the schedule
		o.Log.Info("Creating schedule...")
		if err := o.Client.Create(ctx, schedule); err != nil {
			return fmt.Errorf("failed to create schedule resource: %w", err)
		}
		o.Log.Info("Schedule created successfully", "name", scheduleName, "namespace", o.OADPNamespace, "platform", platform, "schedule", o.Schedule)
	}

	return nil
}

func (o *CreateOptions) validateAndResolveSchedule() error {
	// Check if schedule is a preset and resolve it
	if preset, exists := schedulePresets[strings.ToLower(o.Schedule)]; exists {
		o.Log.Info("Using schedule preset", "preset", o.Schedule, "cron", preset)
		o.Schedule = preset
		return nil
	}

	// If not a preset, validate it's a valid cron expression
	// Basic validation - check it has 5 fields (minute hour day month weekday)
	fields := strings.Fields(o.Schedule)
	if len(fields) != 5 {
		return fmt.Errorf("invalid cron schedule '%s'. Must be in format 'minute hour day month weekday' or use a preset (daily, weekly, monthly, hourly)", o.Schedule)
	}

	return nil
}

// generateScheduleName creates a schedule name using the format: {hcName}-{hcNamespace}-{randomSuffix}
func generateScheduleName(hcName, hcNamespace string, randomGen func(int) string) string {
	randomSuffix := randomGen(6)
	return fmt.Sprintf("%s-%s-schedule-%s", hcName, hcNamespace, randomSuffix)
}

func (o *CreateOptions) generateScheduleObjectWithPlatform(platform string) (*velerov1.Schedule, string, error) {
	// Generate schedule name with random suffix
	var scheduleName string
	if o.ScheduleName != "" {
		scheduleName = o.ScheduleName
	} else {
		scheduleName = generateScheduleName(o.HCName, o.HCNamespace, utilrand.String)
	}

	// Determine which resources to include
	var includedResources []string
	if len(o.IncludedResources) > 0 {
		// Use custom resources provided by user
		includedResources = o.IncludedResources
	} else {
		// Use default resources based on platform
		includedResources = oadp.GetDefaultResourcesForPlatform(platform)
	}

	// Create backup template spec that will be used for each scheduled backup
	backupTemplate := velerov1.BackupSpec{
		IncludedNamespaces: []string{
			o.HCNamespace,
			fmt.Sprintf("%s-%s", o.HCNamespace, o.HCName),
		},
		IncludedResources:        includedResources,
		StorageLocation:          o.StorageLocation,
		TTL:                      metav1.Duration{Duration: o.TTL},
		SnapshotMoveData:         &o.SnapshotMoveData,
		DefaultVolumesToFsBackup: &o.DefaultVolumesToFsBackup,
		DataMover:                "velero",
		SnapshotVolumes:          ptr.To(true),
	}

	// Create schedule object using Velero API
	schedule := &velerov1.Schedule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Schedule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      scheduleName,
			Namespace: o.OADPNamespace,
			Labels: map[string]string{
				"velero.io/storage-location": o.StorageLocation,
				"hypershift.openshift.io/hosted-cluster": o.HCName,
				"hypershift.openshift.io/hosted-cluster-namespace": o.HCNamespace,
			},
		},
		Spec: velerov1.ScheduleSpec{
			Template:                   backupTemplate,
			Schedule:                   o.Schedule,
			Paused:                     o.Paused,
			UseOwnerReferencesInBackup: &o.UseOwnerReferences,
			SkipImmediately:            &o.SkipImmediately,
		},
	}

	return schedule, scheduleName, nil
}

