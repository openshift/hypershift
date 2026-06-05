package oadp

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// VerifyOptions holds configuration for the verify oadp-backup command.
type VerifyOptions struct {
	BackupName    string
	OADPNamespace string
	Log           logr.Logger
	Client        client.Client
}

// VerifyResult captures the outcome of a single verification check.
type VerifyResult struct {
	Check  string
	Passed bool
	Detail string
}

func NewVerifyBackupCommand() *cobra.Command {
	opts := &VerifyOptions{
		Log: log.Log,
	}

	cmd := &cobra.Command{
		Use:   "oadp-backup",
		Short: "Verify integrity of an OADP backup before restoring",
		Long: `Verify the integrity of a Velero backup created for a hosted cluster.

Runs a series of pre-restore checks to ensure the backup is usable:
  - Backup exists and phase is Completed
  - Backup has not expired
  - Backup contains backed-up items
  - Backup storage location is available
  - Reports any warnings or errors from the backup

Examples:
  # Verify a specific backup
  hypershift verify oadp-backup --name example-clusters-lkbtzw

  # Verify with custom OADP namespace
  hypershift verify oadp-backup --name example-clusters-lkbtzw --oadp-namespace custom-adp`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&opts.BackupName, "name", "", "Name of the backup to verify (required)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func (o *VerifyOptions) Run(ctx context.Context) error {
	if o.Client == nil {
		var err error
		o.Client, err = util.GetClient()
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	results, err := VerifyBackup(ctx, o.Client, o.BackupName, o.OADPNamespace, o.Log)
	if err != nil {
		return err
	}

	allPassed := true
	for _, r := range results {
		if r.Passed {
			o.Log.Info("Backup verification", "check", r.Check, "result", "pass", "detail", r.Detail)
		} else {
			o.Log.Info("Backup verification", "check", r.Check, "result", "FAIL", "detail", r.Detail)
			allPassed = false
		}
	}

	if !allPassed {
		return fmt.Errorf("backup verification failed for '%s'", o.BackupName)
	}

	o.Log.Info("Backup verification passed", "backup", o.BackupName)
	return nil
}

// VerifyBackup runs all integrity checks on a backup and returns the results.
// This function is shared between the standalone verify command and the --verify flag on restore.
func VerifyBackup(ctx context.Context, c client.Client, backupName, oadpNamespace string, _ logr.Logger) ([]VerifyResult, error) {
	var results []VerifyResult

	backup := &unstructured.Unstructured{}
	backup.SetAPIVersion("velero.io/v1")
	backup.SetKind("Backup")

	key := client.ObjectKey{Name: backupName, Namespace: oadpNamespace}
	if err := c.Get(ctx, key, backup); err != nil {
		return nil, fmt.Errorf("backup '%s' not found in namespace '%s': %w", backupName, oadpNamespace, err)
	}
	results = append(results, VerifyResult{Check: "exists", Passed: true, Detail: "backup found"})

	// Check phase
	phase, _, _ := unstructured.NestedString(backup.Object, "status", "phase")
	switch phase {
	case "Completed":
		results = append(results, VerifyResult{Check: "phase", Passed: true, Detail: phase})
	case "PartiallyFailed":
		results = append(results, VerifyResult{Check: "phase", Passed: false, Detail: fmt.Sprintf("phase is %s — some items may not have been backed up", phase)})
	case "":
		results = append(results, VerifyResult{Check: "phase", Passed: false, Detail: "phase not set — backup may still be in progress"})
	default:
		results = append(results, VerifyResult{Check: "phase", Passed: false, Detail: fmt.Sprintf("phase is %s", phase)})
	}

	// Check expiration
	expirationStr, expFound, _ := unstructured.NestedString(backup.Object, "status", "expiration")
	if expFound && expirationStr != "" {
		expTime, err := time.Parse(time.RFC3339, expirationStr)
		if err == nil {
			remaining := time.Until(expTime)
			if remaining <= 0 {
				results = append(results, VerifyResult{Check: "expiration", Passed: false, Detail: fmt.Sprintf("backup expired %s ago", (-remaining).Truncate(time.Minute))})
			} else if remaining < time.Hour {
				results = append(results, VerifyResult{Check: "expiration", Passed: true, Detail: fmt.Sprintf("expires in %s (less than 1h remaining)", remaining.Truncate(time.Minute))})
			} else {
				results = append(results, VerifyResult{Check: "expiration", Passed: true, Detail: fmt.Sprintf("expires in %s", remaining.Truncate(time.Minute))})
			}
		}
	}

	// Check items backed up
	itemsBackedUp, itemsFound, _ := unstructured.NestedFieldNoCopy(backup.Object, "status", "progress", "itemsBackedUp")
	if itemsFound {
		count := toInt64(itemsBackedUp)
		if count > 0 {
			results = append(results, VerifyResult{Check: "items", Passed: true, Detail: fmt.Sprintf("%d items backed up", count)})
		} else {
			results = append(results, VerifyResult{Check: "items", Passed: false, Detail: "zero items backed up"})
		}
	} else {
		results = append(results, VerifyResult{Check: "items", Passed: false, Detail: "no progress information available"})
	}

	// Check backup storage location
	storageLocation, slFound, _ := unstructured.NestedString(backup.Object, "spec", "storageLocation")
	if slFound && storageLocation != "" {
		bsl := &unstructured.Unstructured{}
		bsl.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "velero.io",
			Version: "v1",
			Kind:    "BackupStorageLocation",
		})
		bslKey := client.ObjectKey{Name: storageLocation, Namespace: oadpNamespace}
		if err := c.Get(ctx, bslKey, bsl); err != nil {
			results = append(results, VerifyResult{Check: "storage_location", Passed: false, Detail: fmt.Sprintf("BSL '%s' not found: %v", storageLocation, err)})
		} else {
			bslPhase, _, _ := unstructured.NestedString(bsl.Object, "status", "phase")
			if bslPhase == "Available" {
				results = append(results, VerifyResult{Check: "storage_location", Passed: true, Detail: fmt.Sprintf("BSL '%s' is Available", storageLocation)})
			} else {
				results = append(results, VerifyResult{Check: "storage_location", Passed: false, Detail: fmt.Sprintf("BSL '%s' phase is '%s' (expected Available)", storageLocation, bslPhase)})
			}
		}
	}

	// Check warnings and errors
	warnings, _, _ := unstructured.NestedFieldNoCopy(backup.Object, "status", "warnings")
	errors, _, _ := unstructured.NestedFieldNoCopy(backup.Object, "status", "errors")
	warnCount := toInt64(warnings)
	errCount := toInt64(errors)
	if errCount > 0 {
		results = append(results, VerifyResult{Check: "errors", Passed: false, Detail: fmt.Sprintf("%d errors, %d warnings", errCount, warnCount)})
	} else if warnCount > 0 {
		results = append(results, VerifyResult{Check: "warnings", Passed: true, Detail: fmt.Sprintf("%d warnings (no errors)", warnCount)})
	}

	return results, nil
}

func toInt64(val interface{}) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	default:
		return 0
	}
}
