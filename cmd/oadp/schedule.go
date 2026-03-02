package oadp

import (
	"context"
	"fmt"
	"strconv"
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

func NewCreateScheduleCommand() *cobra.Command {
	opts := &CreateOptions{
		Log: log.Log,
	}

	cmd := &cobra.Command{
		Use:   "oadp-schedule",
		Short: "Create a backup schedule for a hosted cluster using OADP",
		Long: `Create a backup schedule for a hosted cluster using OADP (OpenShift API for Data Protection).

The oadp-schedule command automatically detects the platform type of your HostedCluster and creates
scheduled backups using Velero. It validates OADP installation and creates comprehensive backup schedules
that can be used for regular disaster recovery scenarios.

Schedule Format:
The --schedule flag accepts both cron expressions and common Velero verbs:
- Cron expressions: "0 2 * * *" (daily at 2 AM), "0 1 * * 0" (weekly on Sunday at 1 AM)
- Common verbs: daily, weekly, monthly, yearly, hourly
- Velero @ verbs: @daily, @weekly, @monthly, @yearly, @hourly
- Time-specific: daily-2am, daily-6am, daily-noon, weekly-sunday, weekly-friday

Examples:
  # Basic backup schedule with cron expression
  hypershift create oadp-schedule --hc-name prod-cluster --hc-namespace prod-cluster-ns --schedule "0 2 * * *"

  # Daily backup using common verb
  hypershift create oadp-schedule --hc-name prod-cluster --hc-namespace prod-cluster-ns --schedule "daily"

  # Weekly backup with Velero @ verb
  hypershift create oadp-schedule --hc-name dev-cluster --hc-namespace dev-cluster-ns --schedule "@weekly"

  # Monthly backup with custom storage location and TTL
  hypershift create oadp-schedule --hc-name dev-cluster --hc-namespace dev-cluster-ns --schedule "monthly" --storage-location s3-backup --ttl 720h

  # Daily backup at specific time
  hypershift create oadp-schedule --hc-name test-cluster --hc-namespace test-cluster-ns --schedule "daily-6am"

  # Weekly backup on Friday
  hypershift create oadp-schedule --hc-name test-cluster --hc-namespace test-cluster-ns --schedule "weekly-friday"

  # Render schedule YAML without creating it (for GitOps workflows)
  hypershift create oadp-schedule --hc-name test-cluster --hc-namespace test-cluster-ns --schedule "@daily" --render

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.RunSchedule(cmd.Context())
		},
	}

	// Required flags
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Name of the hosted cluster to schedule backups for (required)")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Namespace of the hosted cluster (required)")
	cmd.Flags().StringVar(&opts.Schedule, "schedule", "", "Cron schedule expression for backup frequency (required)")

	// Optional flags
	cmd.Flags().StringVar(&opts.ScheduleName, "name", "", "Custom name for the schedule (auto-generated if not provided)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.StorageLocation, "storage-location", "default", "Backup storage location")
	cmd.Flags().DurationVar(&opts.TTL, "ttl", 2*time.Hour, "Backup retention time (e.g., '24h', '168h' for 7 days, '720h' for 30 days)")
	cmd.Flags().BoolVar(&opts.SnapshotMoveData, "snapshot-move-data", false, "Enable snapshot data movement")
	cmd.Flags().BoolVar(&opts.DefaultVolumesToFsBackup, "default-volumes-to-fs-backup", false, "Enable file system backup for volumes")
	cmd.Flags().StringSliceVar(&opts.IncludedResources, "included-resources", nil, "Override included resources (by default includes platform-specific resources)")
	cmd.Flags().StringSliceVar(&opts.IncludeNamespaces, "include-additional-namespaces", nil, "Additional namespaces to include (HC and HCP namespaces are always included)")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render the schedule object to STDOUT instead of creating it")
	cmd.Flags().BoolVar(&opts.Paused, "paused", false, "Create schedule in paused state")
	cmd.Flags().BoolVar(&opts.UseOwnerReferences, "use-owner-references", false, "Use owner references in backup objects")
	cmd.Flags().BoolVar(&opts.SkipImmediately, "skip-immediately", false, "Skip immediate backup after schedule creation")

	// Mark required flags
	_ = cmd.MarkFlagRequired("hc-name")
	_ = cmd.MarkFlagRequired("hc-namespace")
	_ = cmd.MarkFlagRequired("schedule")

	return cmd
}

// GenerateScheduleName creates a schedule name using the format: {hcName}-{hcNamespace}-{randomSuffix}
// If the name is too long, it uses utils.ShortenName to ensure it doesn't exceed 63 characters
func GenerateScheduleName(hcName, hcNamespace string) string {
	randomSuffix := utilrand.String(6)
	baseName := fmt.Sprintf("%s-%s", hcName, hcNamespace)
	// Use ShortenName to ensure it doesn't exceed DNS1123 subdomain max length (63 chars)
	return utilroute.ShortenName(baseName, randomSuffix, validation.DNS1123LabelMaxLength)
}

func (o *CreateOptions) RunSchedule(ctx context.Context) error {
	// Step 1: Validate the schedule expression
	if err := o.ValidateSchedulePace(); err != nil {
		return fmt.Errorf("schedule validation failed: %w", err)
	}

	// Step 2: Validate schedule name if provided
	if err := o.ValidateScheduleName(); err != nil {
		return err
	}

	// Step 3: Create kubernetes client if not already created
	if o.Client == nil {
		var err error
		o.Client, err = util.GetClient()
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client for schedule validation: %w", err)
		}
	}

	// Step 4: Detect platform (with proper render mode handling)
	var platform string
	if o.Client != nil {
		// Validate HostedCluster exists and get platform
		o.Log.Info("Detecting hosted cluster platform...")
		detectedPlatform, err := oadp.ValidateAndGetHostedClusterPlatform(ctx, o.Client, o.HCName, o.HCNamespace)
		if err != nil {
			if o.Render {
				o.Log.Info("Warning: HostedCluster validation failed, using default platform (AWS)", "error", err.Error())
				platform = "AWS"
			} else {
				return fmt.Errorf("platform detection failed: %w", err)
			}
		} else {
			platform = detectedPlatform
			o.Log.Info("Detected platform", "platform", platform)
		}

		if !o.Render {
			// Step 4: Validate OADP components (only in non-render mode)
			o.Log.Info("Validating OADP components...")
			if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
				return fmt.Errorf("OADP validation failed: %w", err)
			}

			// Step 5: Verify DPA configuration includes HyperShift plugin
			o.Log.Info("Verifying DPA configuration...")
			if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
				return fmt.Errorf("DPA verification failed: %w", err)
			}
		} else {
			// In render mode, run optional validations
			o.Log.Info("Validating OADP components...")
			if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
				o.Log.Info("Warning: OADP validation failed, but continuing with render", "error", err.Error())
			} else {
				o.Log.Info("Verifying DPA configuration...")
				if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
					o.Log.Info("Warning: DPA verification failed, but continuing with render", "error", err.Error())
				}
			}
		}
	} else {
		// No client available (shouldn't happen but just in case)
		platform = "AWS"
	}

	// Step 6: Generate schedule object
	schedule, scheduleName, err := o.GenerateScheduleObject(platform)
	if err != nil {
		return fmt.Errorf("failed to generate schedule object: %w", err)
	}

	// Step 7: Create or render the schedule
	if o.Render {
		return renderYAMLObject(schedule)
	} else {
		// Validate that client is available for creation
		if o.Client == nil {
			return fmt.Errorf("kubernetes client is required for resource creation (not in render mode)")
		}

		o.Log.Info("Creating schedule...")
		if err := o.Client.Create(ctx, schedule); err != nil {
			return fmt.Errorf("failed to create schedule resource: %w", err)
		}
		o.Log.Info("Schedule created successfully", "name", scheduleName, "namespace", o.OADPNamespace, "platform", platform, "schedule", o.Schedule)
	}

	return nil
}

func (o *CreateOptions) GenerateScheduleObject(platform string) (*unstructured.Unstructured, string, error) {
	// Use the name from flag, or generate if empty
	scheduleName := o.ScheduleName
	if scheduleName == "" {
		scheduleName = GenerateScheduleName(o.HCName, o.HCNamespace)
	}

	// Determine which resources to include
	var includedResources []string
	if len(o.IncludedResources) > 0 {
		// Use custom resources provided by user
		includedResources = o.IncludedResources
	} else {
		// Use default resources based on platform
		includedResources = getDefaultResourcesForPlatform(platform)
	}

	// Build included namespaces list
	includedNamespaces := buildIncludedNamespaces(o.HCNamespace, o.HCName, o.IncludeNamespaces)

	// Convert string slices to interface slices for unstructured objects
	includedNamespacesInterface := make([]interface{}, len(includedNamespaces))
	for i, ns := range includedNamespaces {
		includedNamespacesInterface[i] = ns
	}

	includedResourcesInterface := make([]interface{}, len(includedResources))
	for i, res := range includedResources {
		includedResourcesInterface[i] = res
	}

	// Create backup template spec that will be used for each scheduled backup
	backupTemplate := map[string]interface{}{
		"includedNamespaces":       includedNamespacesInterface,
		"includedResources":        includedResourcesInterface,
		"storageLocation":          o.StorageLocation,
		"ttl":                      o.TTL.String(),
		"snapshotMoveData":         o.SnapshotMoveData,
		"defaultVolumesToFsBackup": o.DefaultVolumesToFsBackup,
		"dataMover":                "velero",
		"snapshotVolumes":          true,
	}

	// Create schedule spec
	spec := map[string]interface{}{
		"template":                   backupTemplate,
		"schedule":                   o.Schedule,
		"paused":                     o.Paused,
		"useOwnerReferencesInBackup": o.UseOwnerReferences,
		"skipImmediately":            o.SkipImmediately,
	}

	// Create schedule object using unstructured
	schedule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind":       "Schedule",
			"metadata": map[string]interface{}{
				"name":      scheduleName,
				"namespace": o.OADPNamespace,
				"labels": map[string]interface{}{
					"velero.io/storage-location":                       o.StorageLocation,
					"hypershift.openshift.io/hosted-cluster":           o.HCName,
					"hypershift.openshift.io/hosted-cluster-namespace": o.HCNamespace,
				},
			},
			"spec": spec,
		},
	}

	return schedule, scheduleName, nil
}

func (o *CreateOptions) ValidateSchedulePace() error {
	if o.Schedule == "" {
		return fmt.Errorf("schedule expression is required")
	}

	// Convert common verbs to cron expressions
	normalizedSchedule := normalizeScheduleExpression(o.Schedule)

	// Update the schedule with the normalized value
	o.Schedule = normalizedSchedule

	// Basic validation - check it has 5 fields (minute hour day month weekday)
	fields := strings.Fields(o.Schedule)
	if len(fields) != 5 {
		return fmt.Errorf("invalid cron schedule '%s'. Must be in format 'minute hour day month weekday' (e.g., '0 2 * * *' for daily at 2 AM), or use common verbs like 'daily', 'weekly', '@daily', '@weekly'", o.Schedule)
	}

	// Additional field validation with range checks
	for i, field := range fields {
		if field == "" {
			return fmt.Errorf("invalid cron schedule '%s'. Field %d is empty", o.Schedule, i+1)
		}

		if err := validateCronField(field, i); err != nil {
			return fmt.Errorf("invalid cron schedule '%s'. %w", o.Schedule, err)
		}
	}

	return nil
}

// validateCronField validates a single cron field according to its position and allowed ranges
func validateCronField(field string, position int) error {
	// Define field names and ranges for better error messages
	fieldSpecs := []struct {
		name string
		min  int
		max  int
	}{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day", 1, 31},
		{"month", 1, 12},
		{"weekday", 0, 6},
	}

	spec := fieldSpecs[position]

	// Skip validation for wildcard
	if field == "*" {
		return nil
	}

	// Handle ranges (e.g., "1-5")
	if strings.Contains(field, "-") {
		return validateRange(field, spec.name, spec.min, spec.max)
	}

	// Handle lists (e.g., "1,3,5")
	if strings.Contains(field, ",") {
		return validateList(field, spec.name, spec.min, spec.max)
	}

	// Handle step values (e.g., "*/5" or "1-10/2")
	if strings.Contains(field, "/") {
		return validateStep(field, spec.name, spec.min, spec.max)
	}

	// Handle simple numeric values
	return validateNumber(field, spec.name, spec.min, spec.max)
}

// validateNumber checks if a field is a valid number within range
func validateNumber(field, fieldName string, min, max int) error {
	num, err := strconv.Atoi(field)
	if err != nil {
		return fmt.Errorf("%s field '%s' must be a number", fieldName, field)
	}
	// Use Kubernetes validation utilities for range checking
	if errs := validation.IsInRange(num, min, max); len(errs) > 0 {
		return fmt.Errorf("%s field '%s': %s", fieldName, field, strings.Join(errs, "; "))
	}
	return nil
}

// validateRange validates range expressions like "1-5"
func validateRange(field, fieldName string, min, max int) error {
	parts := strings.Split(field, "-")
	if len(parts) != 2 {
		return fmt.Errorf("%s field '%s' has invalid range format", fieldName, field)
	}

	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("%s field '%s' range start must be a number", fieldName, field)
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("%s field '%s' range end must be a number", fieldName, field)
	}

	// Use Kubernetes validation for range checking
	if errs := validation.IsInRange(start, min, max); len(errs) > 0 {
		return fmt.Errorf("%s field '%s' range start: %s", fieldName, field, strings.Join(errs, "; "))
	}
	if errs := validation.IsInRange(end, min, max); len(errs) > 0 {
		return fmt.Errorf("%s field '%s' range end: %s", fieldName, field, strings.Join(errs, "; "))
	}
	if start > end {
		return fmt.Errorf("%s field '%s' range start cannot be greater than end", fieldName, field)
	}

	return nil
}

// validateList validates comma-separated lists like "1,3,5"
func validateList(field, fieldName string, min, max int) error {
	parts := strings.Split(field, ",")
	if len(parts) == 0 {
		return fmt.Errorf("%s field '%s' has invalid list format", fieldName, field)
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("%s field '%s' has empty list element", fieldName, field)
		}

		// Each part can be a number, range, or step
		if strings.Contains(part, "-") {
			if err := validateRange(part, fieldName, min, max); err != nil {
				return err
			}
		} else if strings.Contains(part, "/") {
			if err := validateStep(part, fieldName, min, max); err != nil {
				return err
			}
		} else {
			if err := validateNumber(part, fieldName, min, max); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateStep validates step expressions like "*/5" or "1-10/2"
func validateStep(field, fieldName string, min, max int) error {
	parts := strings.Split(field, "/")
	if len(parts) != 2 {
		return fmt.Errorf("%s field '%s' has invalid step format", fieldName, field)
	}

	stepValue, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("%s field '%s' step value must be a number", fieldName, field)
	}
	if stepValue <= 0 {
		return fmt.Errorf("%s field '%s' step value must be positive", fieldName, field)
	}

	// Validate the base part (before the slash)
	basePart := parts[0]
	if basePart == "*" {
		// */5 is valid
		return nil
	}

	// Handle range with step like "1-10/2"
	if strings.Contains(basePart, "-") {
		return validateRange(basePart, fieldName, min, max)
	}

	// Handle simple number with step like "5/2" (though this is unusual)
	return validateNumber(basePart, fieldName, min, max)
}

// ValidateScheduleName validates the custom schedule name if provided
func (o *CreateOptions) ValidateScheduleName() error {
	if o.ScheduleName != "" {
		// Kubernetes resource names must be 63 characters or less
		if len(o.ScheduleName) > 63 {
			return fmt.Errorf("schedule name '%s' is too long (%d characters). Kubernetes resource names must be 63 characters or less. Use --name to specify a shorter custom name", o.ScheduleName, len(o.ScheduleName))
		}
		// Use Kubernetes official DNS subdomain validation
		if errs := validation.IsDNS1123Subdomain(o.ScheduleName); len(errs) > 0 {
			return fmt.Errorf("schedule name '%s' is invalid: %s", o.ScheduleName, strings.Join(errs, "; "))
		}
	}
	return nil
}

// normalizeScheduleExpression converts common Velero schedule verbs to cron expressions
func normalizeScheduleExpression(schedule string) string {
	// Trim whitespace and convert to lowercase for comparison
	normalizedInput := strings.TrimSpace(strings.ToLower(schedule))

	// Map of common schedule verbs to cron expressions
	scheduleVerbs := map[string]string{
		// Standard @ verbs (commonly used in Velero)
		"@yearly":   "0 0 1 1 *", // January 1st at midnight
		"@annually": "0 0 1 1 *", // Same as @yearly
		"@monthly":  "0 0 1 * *", // 1st day of month at midnight
		"@weekly":   "0 0 * * 0", // Sunday at midnight
		"@daily":    "0 0 * * *", // Every day at midnight
		"@midnight": "0 0 * * *", // Same as @daily
		"@hourly":   "0 * * * *", // Every hour at minute 0

		// Simple verbs (user-friendly)
		"yearly":   "0 0 1 1 *", // January 1st at midnight
		"annually": "0 0 1 1 *", // Same as yearly
		"monthly":  "0 0 1 * *", // 1st day of month at midnight
		"weekly":   "0 0 * * 0", // Sunday at midnight
		"daily":    "0 0 * * *", // Every day at midnight
		"hourly":   "0 * * * *", // Every hour at minute 0

		// Alternative daily schedules with different times
		"daily-1am":  "0 1 * * *",  // Every day at 1 AM
		"daily-2am":  "0 2 * * *",  // Every day at 2 AM
		"daily-3am":  "0 3 * * *",  // Every day at 3 AM
		"daily-6am":  "0 6 * * *",  // Every day at 6 AM
		"daily-noon": "0 12 * * *", // Every day at noon

		// Alternative weekly schedules
		"weekly-sunday":   "0 0 * * 0", // Sunday at midnight
		"weekly-monday":   "0 0 * * 1", // Monday at midnight
		"weekly-friday":   "0 0 * * 5", // Friday at midnight
		"weekly-saturday": "0 0 * * 6", // Saturday at midnight
	}

	// Look up the normalized input in our map
	if cronExpr, found := scheduleVerbs[normalizedInput]; found {
		return cronExpr
	}

	// If not found in map, return original schedule (might be a valid cron expression)
	return schedule
}
