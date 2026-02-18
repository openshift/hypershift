//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openshift/hypershift/test/e2e/v2/internal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// veleroNamespace is the namespace where Velero is deployed
	veleroNamespace = "openshift-adp"

	// Velero backup phase constants
	BackupPhaseNew                                       = "New"
	BackupPhaseQueued                                    = ""
	BackupPhaseReadyToStart                              = "ReadyToStart"
	BackupPhaseInProgress                                = "InProgress"
	BackupPhaseWaitingForPluginOperations                = "WaitingForPluginOperations"
	BackupPhaseWaitingForPluginOperationsPartiallyFailed = "WaitingForPluginOperationsPartiallyFailed"
	BackupPhaseFinalizing                                = "Finalizing"
	BackupPhaseFinalizingPartiallyFailed                 = "FinalizingPartiallyFailed"
	BackupPhaseCompleted                                 = "Completed"
	BackupPhaseFailed                                    = "Failed"
	BackupPhasePartiallyFailed                           = "PartiallyFailed"
	BackupPhaseDeleting                                  = "Deleting"

	// Velero restore phase constants
	RestorePhaseNew                                       = "New"
	RestorePhaseInProgress                                = "InProgress"
	RestorePhaseWaitingForPluginOperations                = "WaitingForPluginOperations"
	RestorePhaseWaitingForPluginOperationsPartiallyFailed = "WaitingForPluginOperationsPartiallyFailed"
	RestorePhaseFinalizing                                = "Finalizing"
	RestorePhaseFinalizingPartiallyFailed                 = "FinalizingPartiallyFailed"
	RestorePhaseCompleted                                 = "Completed"
	RestorePhaseFailed                                    = "Failed"
	RestorePhasePartiallyFailed                           = "PartiallyFailed"
)

// EnsureVeleroPodRunning checks if the Velero pod is running in the specified namespace.
func EnsureVeleroPodRunning(testCtx *internal.TestContext) error {
	client := testCtx.MgmtClient
	podList := &corev1.PodList{}
	labels := map[string]string{
		"deploy":    "velero",
		"component": "velero",
	}

	if err := client.List(testCtx.Context, podList, crclient.InNamespace(veleroNamespace), crclient.MatchingLabels(labels)); err != nil {
		return fmt.Errorf("failed to list Velero pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return fmt.Errorf("no Velero pod found in namespace %s", veleroNamespace)
	}

	if len(podList.Items) > 1 {
		return fmt.Errorf("more than one Velero pod found in namespace %s", veleroNamespace)
	}

	pod := &podList.Items[0]
	if pod.Status.Phase != corev1.PodRunning {
		return fmt.Errorf("Velero pod is not running, current phase: %s", pod.Status.Phase)
	}

	return nil
}

// WaitForBackupCompletion waits for a backup to complete.
// If backupName is provided, it waits for that specific backup.
// If backupName is empty, it finds the most recent backup matching the HostedCluster name/namespace.
func WaitForBackupCompletion(testCtx *internal.TestContext, backupName string) error {
	// If no backup name provided, find the most recent backup matching the prefix
	if backupName == "" {
		var err error
		backupName, err = getLatestBackupForHostedCluster(testCtx.Context, testCtx.MgmtClient, veleroNamespace, testCtx.ClusterName, testCtx.ClusterNamespace)
		if err != nil {
			return fmt.Errorf("failed to find backup for HostedCluster %s/%s: %w", testCtx.ClusterNamespace, testCtx.ClusterName, err)
		}
	}

	// Wait for backup to reach a final state
	checkFn := isBackupInFinalState(testCtx.MgmtClient, veleroNamespace, backupName)
	err := wait.PollUntilContextTimeout(testCtx.Context, 10*time.Second, BackupTimeout, true, func(ctx context.Context) (bool, error) {
		return checkFn(ctx)
	})
	if err != nil {
		return fmt.Errorf("backup %s did not reach final state within %v: %w", backupName, BackupTimeout, err)
	}

	return ensureBackupSuccessful(testCtx.Context, testCtx.MgmtClient, veleroNamespace, backupName)
}

// ensureBackupSuccessful verifies that a backup completed successfully.
func ensureBackupSuccessful(ctx context.Context, client crclient.Client, oadpNamespace, backupName string) error {
	backup, err := getBackup(ctx, client, oadpNamespace, backupName)
	if err != nil {
		return fmt.Errorf("failed to get backup %s: %w", backupName, err)
	}

	phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get backup phase: %w", err)
	}
	if !found {
		// This is expected temporarily
		return nil
	}
	if phase != BackupPhaseCompleted {
		failureReason, _, _ := unstructured.NestedString(backup.Object, "status", "failureReason")
		validationErrors, _, _ := unstructured.NestedStringSlice(backup.Object, "status", "validationErrors")
		return fmt.Errorf("backup %s did not complete successfully: phase=%s, failureReason=%s, validationErrors=%v",
			backupName, phase, failureReason, validationErrors)
	}

	return nil
}

// getLatestBackupForHostedCluster finds the most recent backup matching the hcName-hcNamespace prefix
func getLatestBackupForHostedCluster(ctx context.Context, client crclient.Client, oadpNamespace, hcName, hcNamespace string) (string, error) {
	backupList := &unstructured.UnstructuredList{}
	backupList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "BackupList",
	})
	if err := client.List(ctx, backupList, crclient.InNamespace(oadpNamespace)); err != nil {
		return "", fmt.Errorf("failed to list backups: %w", err)
	}

	// Filter backups by prefix and collect matching ones
	prefix := fmt.Sprintf("%s-%s-", hcName, hcNamespace)
	var matchingBackups []unstructured.Unstructured
	for _, backup := range backupList.Items {
		if strings.HasPrefix(backup.GetName(), prefix) {
			matchingBackups = append(matchingBackups, backup)
		}
	}

	if len(matchingBackups) == 0 {
		return "", fmt.Errorf("no backups found with prefix %s in namespace %s", prefix, oadpNamespace)
	}

	// Sort by creation timestamp (most recent first)
	sort.Slice(matchingBackups, func(i, j int) bool {
		return matchingBackups[i].GetCreationTimestamp().After(matchingBackups[j].GetCreationTimestamp().Time)
	})

	return matchingBackups[0].GetName(), nil
}

// WaitForScheduleCompletion waits for the most recent backup created by a schedule to complete.
// It finds the latest backup with label velero.io/schedule-name: scheduleName and waits for it to finish.
func WaitForScheduleCompletion(testCtx *internal.TestContext, scheduleName string) error {
	// Find the most recent backup created by this schedule
	backupName, err := getLatestBackupForSchedule(testCtx.Context, testCtx.MgmtClient, veleroNamespace, scheduleName)
	if err != nil {
		return fmt.Errorf("failed to find backup for schedule %s: %w", scheduleName, err)
	}

	// Wait for backup to reach a final state
	checkFn := isBackupInFinalState(testCtx.MgmtClient, veleroNamespace, backupName)
	if err := wait.PollUntilContextTimeout(testCtx.Context, PollInterval, BackupTimeout, true, func(ctx context.Context) (bool, error) {
		return checkFn(ctx)
	}); err != nil {
		return fmt.Errorf("backup %s (from schedule %s) did not reach final state within %v: %w", backupName, scheduleName, BackupTimeout, err)
	}

	return ensureBackupSuccessful(testCtx.Context, testCtx.MgmtClient, veleroNamespace, backupName)
}

// getLatestBackupForSchedule finds the most recent backup with label velero.io/schedule-name: scheduleName.
// It waits for a backup to exist before returning, polling until a backup is created by the schedule.
func getLatestBackupForSchedule(ctx context.Context, client crclient.Client, oadpNamespace string, scheduleName string) (string, error) {
	var backupName string
	// Wait for a backup to be created by the schedule
	err := wait.PollUntilContextTimeout(ctx, PollInterval, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		backupList := &unstructured.UnstructuredList{}
		backupList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "velero.io",
			Version: "v1",
			Kind:    "BackupList",
		})

		// List backups with the schedule label
		labelSelector := crclient.MatchingLabels{
			"velero.io/schedule-name": scheduleName,
		}
		if lastErr := client.List(ctx, backupList, crclient.InNamespace(oadpNamespace), labelSelector); lastErr != nil {
			return false, lastErr
		}
		if len(backupList.Items) == 0 {
			return false, nil
		}

		// Sort by creation timestamp (most recent first)
		sort.Slice(backupList.Items, func(i, j int) bool {
			return backupList.Items[i].GetCreationTimestamp().After(backupList.Items[j].GetCreationTimestamp().Time)
		})

		backupName = backupList.Items[0].GetName()
		return true, nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to find backup for schedule %s within timeout: %w", scheduleName, err)
	}

	return backupName, nil
}

// getVeleroResource retrieves a Velero resource (Backup, Restore, or Schedule) by name.
// This is a generic helper to avoid code duplication across resource types.
func getVeleroResource(ctx context.Context, client crclient.Client, namespace, name, kind string) (*unstructured.Unstructured, error) {
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    kind,
	})
	err := client.Get(ctx, crclient.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, resource)
	if err != nil {
		return nil, err
	}
	return resource, nil
}

// getBackup retrieves a backup by name
func getBackup(ctx context.Context, client crclient.Client, namespace, name string) (*unstructured.Unstructured, error) {
	return getVeleroResource(ctx, client, namespace, name, "Backup")
}

// isBackupInFinalState returns a function that checks if a backup is in a final state. This
// can be both success and failure.
func isBackupInFinalState(client crclient.Client, veleroNamespace, name string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		backup, err := getBackup(ctx, client, veleroNamespace, name)
		if err != nil {
			return false, fmt.Errorf("failed to get backup %s: %w", name, err)
		}

		phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
		if err != nil {
			return false, fmt.Errorf("failed to get backup phase: %w", err)
		}

		if !found {
			// This is expected temporarily
			return false, nil
		}

		// List of phases that indicate the backup is not done
		phasesNotDone := []string{
			BackupPhaseNew,
			BackupPhaseQueued,
			BackupPhaseReadyToStart,
			BackupPhaseInProgress,
			BackupPhaseWaitingForPluginOperations,
			BackupPhaseWaitingForPluginOperationsPartiallyFailed,
			BackupPhaseFinalizing,
			BackupPhaseFinalizingPartiallyFailed,
		}

		for _, notDonePhase := range phasesNotDone {
			if phase == notDonePhase {
				return false, nil
			}
		}
		return true, nil
	}
}

// WaitForRestoreCompletion waits for a restore to complete.
// If restoreName is provided, it waits for that specific restore.
// If restoreName is empty, it finds the most recent restore matching the HostedCluster name/namespace.
func WaitForRestoreCompletion(testCtx *internal.TestContext, restoreName string) error {
	// If no restore name provided, find the most recent restore matching the prefix
	if restoreName == "" {
		var err error
		restoreName, err = getLatestRestoreForHostedCluster(testCtx.Context, testCtx.MgmtClient, veleroNamespace, testCtx.ClusterName, testCtx.ClusterNamespace)
		if err != nil {
			return fmt.Errorf("failed to find restore for HostedCluster %s/%s: %w", testCtx.ClusterNamespace, testCtx.ClusterName, err)
		}
	}

	// Wait for restore to reach a final state
	checkFn := isRestoreInFinalState(testCtx.MgmtClient, veleroNamespace, restoreName)
	if err := wait.PollUntilContextTimeout(testCtx.Context, 10*time.Second, RestoreTimeout, true, func(ctx context.Context) (bool, error) {
		return checkFn(ctx)
	}); err != nil {
		return fmt.Errorf("restore %s did not reach final state within %v: %w", restoreName, RestoreTimeout, err)
	}

	return ensureRestoreSuccessful(testCtx.Context, testCtx.MgmtClient, veleroNamespace, restoreName)
}

// ensureRestoreSuccessful verifies that a restore completed successfully.
func ensureRestoreSuccessful(ctx context.Context, client crclient.Client, oadpNamespace, restoreName string) error {
	restore, err := getRestore(ctx, client, oadpNamespace, restoreName)
	if err != nil {
		return fmt.Errorf("failed to get restore %s: %w", restoreName, err)
	}

	phase, found, err := unstructured.NestedString(restore.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get restore phase: %w", err)
	}
	if !found {
		// This is expected temporarily
		return nil
	}
	if phase != RestorePhaseCompleted {
		failureReason, _, _ := unstructured.NestedString(restore.Object, "status", "failureReason")
		validationErrors, _, _ := unstructured.NestedStringSlice(restore.Object, "status", "validationErrors")
		warnings, _, _ := unstructured.NestedInt64(restore.Object, "status", "warnings")
		errors, _, _ := unstructured.NestedInt64(restore.Object, "status", "errors")
		return fmt.Errorf("restore %s did not complete successfully: phase=%s, failureReason=%s, validationErrors=%v, warnings=%d, errors=%d",
			restoreName, phase, failureReason, validationErrors, warnings, errors)
	}

	return nil
}

// getLatestRestoreForHostedCluster finds the most recent restore matching the hcName-hcNamespace prefix
func getLatestRestoreForHostedCluster(ctx context.Context, client crclient.Client, oadpNamespace, hcName, hcNamespace string) (string, error) {
	restoreList := &unstructured.UnstructuredList{}
	restoreList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "RestoreList",
	})
	if err := client.List(ctx, restoreList, crclient.InNamespace(oadpNamespace)); err != nil {
		return "", fmt.Errorf("failed to list restores: %w", err)
	}

	// Filter restores by prefix and collect matching ones
	prefix := fmt.Sprintf("%s-%s-", hcName, hcNamespace)
	var matchingRestores []unstructured.Unstructured
	for _, restore := range restoreList.Items {
		if strings.HasPrefix(restore.GetName(), prefix) {
			matchingRestores = append(matchingRestores, restore)
		}
	}

	if len(matchingRestores) == 0 {
		return "", fmt.Errorf("no restores found with prefix %s in namespace %s", prefix, oadpNamespace)
	}

	// Sort by creation timestamp (most recent first)
	sort.Slice(matchingRestores, func(i, j int) bool {
		return matchingRestores[i].GetCreationTimestamp().After(matchingRestores[j].GetCreationTimestamp().Time)
	})

	return matchingRestores[0].GetName(), nil
}

// getRestore retrieves a restore by name
func getRestore(ctx context.Context, client crclient.Client, namespace, name string) (*unstructured.Unstructured, error) {
	return getVeleroResource(ctx, client, namespace, name, "Restore")
}

// isRestoreInFinalState returns a function that checks if a restore is in a final state.
// This can be both success and failure.
func isRestoreInFinalState(client crclient.Client, veleroNamespace, name string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		restore, err := getRestore(ctx, client, veleroNamespace, name)
		if err != nil {
			return false, fmt.Errorf("failed to get restore %s: %w", name, err)
		}

		phase, found, err := unstructured.NestedString(restore.Object, "status", "phase")
		if err != nil {
			return false, fmt.Errorf("failed to get restore phase: %w", err)
		}
		if !found {
			// This is expected temporarily
			return false, nil
		}

		// List of phases that indicate the restore is not done
		phasesNotDone := []string{
			RestorePhaseNew,
			RestorePhaseInProgress,
			RestorePhaseWaitingForPluginOperations,
			RestorePhaseWaitingForPluginOperationsPartiallyFailed,
			RestorePhaseFinalizing,
			RestorePhaseFinalizingPartiallyFailed,
		}

		for _, notDonePhase := range phasesNotDone {
			if phase == notDonePhase {
				return false, nil
			}
		}
		return true, nil
	}
}

// DeleteOADPSchedule deletes a Velero Schedule resource.
func DeleteOADPSchedule(testCtx *internal.TestContext, scheduleName string) error {

	// Now delete the Schedule
	schedule := &unstructured.Unstructured{}
	schedule.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "Schedule",
	})

	err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: veleroNamespace,
		Name:      scheduleName,
	}, schedule)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already deleted
			return nil
		}
		return fmt.Errorf("failed to get Schedule %s/%s: %w", veleroNamespace, scheduleName, err)
	}

	// Delete the Schedule
	if err := testCtx.MgmtClient.Delete(testCtx.Context, schedule); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete Schedule %s/%s: %w", veleroNamespace, scheduleName, err)
		}
	}

	return nil
}
