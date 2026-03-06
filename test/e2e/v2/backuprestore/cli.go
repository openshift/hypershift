//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/test/integration/framework"
)

const (
	BackupTimeout        = 20 * time.Minute
	RestoreTimeout       = 20 * time.Minute
	DefaultOADPNamespace = "openshift-adp"
)

var (
	// hypershiftCLIPath is cached to avoid repeated lookups
	hypershiftCLIPath string
	// hypershiftCLIOnce ensures we only look up the CLI path once
	hypershiftCLIOnce sync.Once
	// hypershiftCLIErr stores any error from CLI lookup
	hypershiftCLIErr error
)

// getHypershiftCLIPath returns the path to the hypershift CLI binary.
// It uses exec.LookPath to find it and caches the result.
func getHypershiftCLIPath() (string, error) {
	hypershiftCLIOnce.Do(func() {
		hypershiftCLIPath, hypershiftCLIErr = exec.LookPath("hypershift")
		if hypershiftCLIErr != nil || hypershiftCLIPath == "" {
			hypershiftCLIErr = fmt.Errorf("cannot find hypershift command: %w", hypershiftCLIErr)
		}
	})
	return hypershiftCLIPath, hypershiftCLIErr
}

// OADPBackupOptions contains options for creating an OADP backup
type OADPBackupOptions struct {
	// Required
	HCName      string
	HCNamespace string

	// Optional
	Name                     string   // Custom name for the backup
	OADPNamespace            string   // Namespace where OADP operator is installed (default: openshift-adp)
	StorageLocation          string   // Storage location for the backup (default: default)
	TTL                      string   // Time to live for the backup (default: 2h)
	SnapshotMoveData         *bool    // Enable snapshot move data feature (default: true)
	DefaultVolumesToFsBackup bool     // Use filesystem backup for volumes by default
	Render                   bool     // Render the backup object to STDOUT instead of creating it
	IncludedResources        []string // Comma-separated list of resources to include
	IncludeNamespaces        []string // Additional namespaces to include
}

// OADPRestoreOptions contains options for creating an OADP restore
type OADPRestoreOptions struct {
	// Required
	HCName      string
	HCNamespace string

	// One of these is required
	FromBackup   string // Name of the backup to restore from
	FromSchedule string // Name of the schedule to restore from

	// Optional
	Name                   string   // Custom name for the restore
	OADPNamespace          string   // Namespace where OADP operator is installed (default: openshift-adp)
	ExistingResourcePolicy string   // Policy for handling existing resources (default: update)
	IncludeNamespaces      []string // Additional namespaces to include
	Render                 bool     // Render the restore object to STDOUT instead of creating it
	RestorePVs             *bool    // Restore persistent volumes (default: true)
	PreserveNodePorts      *bool    // Preserve NodePort assignments during restore (default: true)
}

// OADPScheduleOptions contains options for creating an OADP backup schedule
type OADPScheduleOptions struct {
	// Required
	HCName      string
	HCNamespace string
	Schedule    string // Cron schedule expression or common verb (daily, weekly, etc.)

	// Optional
	Name                     string   // Custom name for the schedule
	OADPNamespace            string   // Namespace where OADP operator is installed (default: openshift-adp)
	StorageLocation          string   // Backup storage location (default: default)
	TTL                      string   // Backup retention time (default: 2h)
	SnapshotMoveData         *bool    // Enable snapshot data movement (default: true)
	DefaultVolumesToFsBackup bool     // Enable file system backup for volumes
	IncludedResources        []string // Override included resources
	IncludeNamespaces        []string // Additional namespaces to include
	Render                   bool     // Render the schedule object to STDOUT instead of creating it
	Paused                   bool     // Create schedule in paused state
	UseOwnerReferences       bool     // Use owner references in backup objects
	SkipImmediately          bool     // Skip immediate backup after schedule creation
}

// RunOADPBackup executes the "hypershift create oadp-backup" command
func RunOADPBackup(ctx context.Context, logger logr.Logger, artifactDir string, backupOpts *OADPBackupOptions) error {
	if backupOpts.HCName == "" || backupOpts.HCNamespace == "" {
		return fmt.Errorf("hc-name and hc-namespace are required")
	}

	hypershiftCLI, err := getHypershiftCLIPath()
	if err != nil {
		return err
	}

	if artifactDir == "" {
		return fmt.Errorf("artifact directory is required")
	}

	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}

	args := buildBackupArgs(backupOpts)
	cmd := exec.CommandContext(ctx, hypershiftCLI, args...)

	logPath := fmt.Sprintf("oadp-backup-%s-%s.log", backupOpts.HCNamespace, backupOpts.HCName)

	// Create minimal framework options for RunCommand
	opts := &framework.Options{
		ArtifactDir: artifactDir,
	}

	return framework.RunCommand(logger, opts, logPath, cmd)
}

// RunOADPRestore executes the "hypershift create oadp-restore" command
func RunOADPRestore(ctx context.Context, logger logr.Logger, artifactDir string, restoreOpts *OADPRestoreOptions) error {
	if restoreOpts.HCName == "" || restoreOpts.HCNamespace == "" {
		return fmt.Errorf("hc-name and hc-namespace are required")
	}

	if restoreOpts.FromBackup == "" && restoreOpts.FromSchedule == "" {
		return fmt.Errorf("either from-backup or from-schedule is required")
	}

	if restoreOpts.FromBackup != "" && restoreOpts.FromSchedule != "" {
		return fmt.Errorf("from-backup and from-schedule are mutually exclusive")
	}

	hypershiftCLI, err := getHypershiftCLIPath()
	if err != nil {
		return err
	}

	if artifactDir == "" {
		return fmt.Errorf("artifact directory is required")
	}

	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}

	args := buildRestoreArgs(restoreOpts)
	cmd := exec.CommandContext(ctx, hypershiftCLI, args...)

	logPath := fmt.Sprintf("oadp-restore-%s-%s.log", restoreOpts.HCNamespace, restoreOpts.HCName)

	// Create minimal framework options for RunCommand
	opts := &framework.Options{
		ArtifactDir: artifactDir,
	}

	return framework.RunCommand(logger, opts, logPath, cmd)
}

// RunOADPSchedule executes the "hypershift create oadp-schedule" command
func RunOADPSchedule(ctx context.Context, logger logr.Logger, artifactDir string, scheduleOpts *OADPScheduleOptions) error {
	if scheduleOpts.HCName == "" || scheduleOpts.HCNamespace == "" {
		return fmt.Errorf("hc-name and hc-namespace are required")
	}

	if scheduleOpts.Schedule == "" {
		return fmt.Errorf("schedule is required")
	}

	hypershiftCLI, err := getHypershiftCLIPath()
	if err != nil {
		return err
	}

	if artifactDir == "" {
		return fmt.Errorf("artifact directory is required")
	}

	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}

	args := buildScheduleArgs(scheduleOpts)
	cmd := exec.CommandContext(ctx, hypershiftCLI, args...)

	logPath := fmt.Sprintf("oadp-schedule-%s-%s.log", scheduleOpts.HCNamespace, scheduleOpts.HCName)

	// Create minimal framework options for RunCommand
	opts := &framework.Options{
		ArtifactDir: artifactDir,
	}

	return framework.RunCommand(logger, opts, logPath, cmd)
}

// buildBackupArgs constructs the command line arguments for oadp-backup
func buildBackupArgs(opts *OADPBackupOptions) []string {
	args := []string{"create", "oadp-backup"}

	// Required flags
	args = append(args, "--hc-name", opts.HCName)
	args = append(args, "--hc-namespace", opts.HCNamespace)

	// Optional flags with custom values
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if opts.OADPNamespace != "" {
		args = append(args, "--oadp-namespace", opts.OADPNamespace)
	}
	if opts.StorageLocation != "" {
		args = append(args, "--storage-location", opts.StorageLocation)
	}
	if opts.TTL != "" {
		args = append(args, "--ttl", opts.TTL)
	}

	// Boolean flags - only set if explicitly provided
	if opts.SnapshotMoveData != nil {
		if *opts.SnapshotMoveData {
			args = append(args, "--snapshot-move-data=true")
		} else {
			args = append(args, "--snapshot-move-data=false")
		}
	}
	if opts.DefaultVolumesToFsBackup {
		args = append(args, "--default-volumes-to-fs-backup=true")
	}
	if opts.Render {
		args = append(args, "--render")
	}

	// Slice flags
	if len(opts.IncludedResources) > 0 {
		args = append(args, "--included-resources", strings.Join(opts.IncludedResources, ","))
	}
	if len(opts.IncludeNamespaces) > 0 {
		args = append(args, "--include-additional-namespaces", strings.Join(opts.IncludeNamespaces, ","))
	}

	return args
}

// buildRestoreArgs constructs the command line arguments for oadp-restore
func buildRestoreArgs(opts *OADPRestoreOptions) []string {
	args := []string{"create", "oadp-restore"}

	// Required flags
	args = append(args, "--hc-name", opts.HCName)
	args = append(args, "--hc-namespace", opts.HCNamespace)

	// Backup source (one required)
	if opts.FromBackup != "" {
		args = append(args, "--from-backup", opts.FromBackup)
	}
	if opts.FromSchedule != "" {
		args = append(args, "--from-schedule", opts.FromSchedule)
	}

	// Optional flags with custom values
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if opts.OADPNamespace != "" {
		args = append(args, "--oadp-namespace", opts.OADPNamespace)
	}
	if opts.ExistingResourcePolicy != "" {
		args = append(args, "--existing-resource-policy", opts.ExistingResourcePolicy)
	}

	// Boolean flags - only set if explicitly provided
	if opts.RestorePVs != nil {
		if *opts.RestorePVs {
			args = append(args, "--restore-pvs=true")
		} else {
			args = append(args, "--restore-pvs=false")
		}
	}
	if opts.PreserveNodePorts != nil {
		if *opts.PreserveNodePorts {
			args = append(args, "--preserve-node-ports=true")
		} else {
			args = append(args, "--preserve-node-ports=false")
		}
	}
	if opts.Render {
		args = append(args, "--render")
	}

	// Slice flags
	if len(opts.IncludeNamespaces) > 0 {
		args = append(args, "--include-additional-namespaces", strings.Join(opts.IncludeNamespaces, ","))
	}

	return args
}

// buildScheduleArgs constructs the command line arguments for oadp-schedule
func buildScheduleArgs(opts *OADPScheduleOptions) []string {
	args := []string{"create", "oadp-schedule"}

	// Required flags
	args = append(args, "--hc-name", opts.HCName)
	args = append(args, "--hc-namespace", opts.HCNamespace)
	args = append(args, "--schedule", opts.Schedule)

	// Optional flags with custom values
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if opts.OADPNamespace != "" {
		args = append(args, "--oadp-namespace", opts.OADPNamespace)
	}
	if opts.StorageLocation != "" {
		args = append(args, "--storage-location", opts.StorageLocation)
	}
	if opts.TTL != "" {
		args = append(args, "--ttl", opts.TTL)
	}

	// Boolean flags - only set if explicitly provided or true (non-default)
	if opts.SnapshotMoveData != nil {
		if *opts.SnapshotMoveData {
			args = append(args, "--snapshot-move-data=true")
		} else {
			args = append(args, "--snapshot-move-data=false")
		}
	}
	if opts.DefaultVolumesToFsBackup {
		args = append(args, "--default-volumes-to-fs-backup=true")
	}
	if opts.Render {
		args = append(args, "--render")
	}
	if opts.Paused {
		args = append(args, "--paused")
	}
	if opts.UseOwnerReferences {
		args = append(args, "--use-owner-references")
	}
	if opts.SkipImmediately {
		args = append(args, "--skip-immediately")
	}

	// Slice flags
	if len(opts.IncludedResources) > 0 {
		args = append(args, "--included-resources", strings.Join(opts.IncludedResources, ","))
	}
	if len(opts.IncludeNamespaces) > 0 {
		args = append(args, "--include-additional-namespaces", strings.Join(opts.IncludeNamespaces, ","))
	}

	return args
}
