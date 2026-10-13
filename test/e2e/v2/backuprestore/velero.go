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
	// Velero backup phase constants
	BackupPhaseNew                                       = "New"
	BackupPhaseQueued                                    = "Queued"
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

// EnsureVeleroPodRunning checks if at least one Velero pod is running and ready in the specified namespace.
// This function tolerates multiple Velero pods (e.g., during rollouts or restarts) as long as at least one is healthy.
func EnsureVeleroPodRunning(testCtx *internal.TestContext) error {
	client := testCtx.MgmtClient
	podList := &corev1.PodList{}
	labels := map[string]string{
		"deploy":    "velero",
		"component": "velero",
	}

	if err := client.List(testCtx.Context, podList, crclient.InNamespace(DefaultOADPNamespace), crclient.MatchingLabels(labels)); err != nil {
		return fmt.Errorf("failed to list Velero pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return fmt.Errorf("no Velero pod found in namespace %s", DefaultOADPNamespace)
	}

	// Check if at least one pod is running and ready
	// Multiple pods can exist during rollouts or restarts, which is acceptable
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning && isPodReady(&pod) {
			return nil
		}
	}

	// No running and ready pod found - collect pod states for error message
	var podStates []string
	for _, pod := range podList.Items {
		readyStatus := "not ready"
		if isPodReady(&pod) {
			readyStatus = "ready"
		}
		podStates = append(podStates, fmt.Sprintf("%s: phase=%s, %s", pod.Name, pod.Status.Phase, readyStatus))
	}

	return fmt.Errorf("no running and ready Velero pod found in namespace %s (found %d pod(s): %v)",
		DefaultOADPNamespace, len(podList.Items), podStates)
}

// isPodReady checks if a pod has the Ready condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// WaitForBackupCompletion waits for a backup to complete.
// If backupName is provided, it waits for that specific backup.
// If backupName is empty, it finds the most recent backup matching the HostedCluster name/namespace.
func WaitForBackupCompletion(testCtx *internal.TestContext, backupName string) error {
	// If no backup name provided, find the most recent backup matching the prefix
	if backupName == "" {
		var err error
		backupName, err = getLatestBackupForHostedCluster(testCtx.Context, testCtx.MgmtClient, DefaultOADPNamespace, testCtx.ClusterName, testCtx.ClusterNamespace)
		if err != nil {
			return fmt.Errorf("failed to find backup for HostedCluster %s/%s: %w", testCtx.ClusterNamespace, testCtx.ClusterName, err)
		}
	}

	// Wait for backup to reach a final state
	checkFn := isBackupInFinalState(testCtx.MgmtClient, DefaultOADPNamespace, backupName)
	err := wait.PollUntilContextTimeout(testCtx.Context, 10*time.Second, BackupTimeout, true, func(ctx context.Context) (bool, error) {
		return checkFn(ctx)
	})
	if err != nil {
		return fmt.Errorf("backup %s did not reach final state within %v: %w", backupName, BackupTimeout, err)
	}

	return ensureBackupSuccessful(testCtx.Context, testCtx.MgmtClient, DefaultOADPNamespace, backupName)
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

// getLatestVeleroResourceForHostedCluster finds the most recent Velero resource (Backup or Restore)
// matching the hcName-hcNamespace prefix
func getLatestVeleroResourceForHostedCluster(ctx context.Context, client crclient.Client, oadpNamespace, hcName, hcNamespace, kind, listKind string) (string, error) {
	resourceList := &unstructured.UnstructuredList{}
	resourceList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    listKind,
	})
	if err := client.List(ctx, resourceList, crclient.InNamespace(oadpNamespace)); err != nil {
		return "", fmt.Errorf("failed to list %ss: %w", kind, err)
	}

	// Filter resources by prefix and collect matching ones
	prefix := fmt.Sprintf("%s-%s-", hcName, hcNamespace)
	var matchingResources []unstructured.Unstructured
	for _, resource := range resourceList.Items {
		if strings.HasPrefix(resource.GetName(), prefix) {
			matchingResources = append(matchingResources, resource)
		}
	}

	if len(matchingResources) == 0 {
		return "", fmt.Errorf("no %ss found with prefix %s in namespace %s", kind, prefix, oadpNamespace)
	}

	// Sort by creation timestamp (most recent first)
	sort.Slice(matchingResources, func(i, j int) bool {
		return matchingResources[i].GetCreationTimestamp().After(matchingResources[j].GetCreationTimestamp().Time)
	})

	return matchingResources[0].GetName(), nil
}

// getLatestBackupForHostedCluster finds the most recent backup matching the hcName-hcNamespace prefix
func getLatestBackupForHostedCluster(ctx context.Context, client crclient.Client, oadpNamespace, hcName, hcNamespace string) (string, error) {
	return getLatestVeleroResourceForHostedCluster(ctx, client, oadpNamespace, hcName, hcNamespace, "Backup", "BackupList")
}

// WaitForScheduleCompletion waits for the most recent backup created by a schedule to complete.
// It finds the latest backup with label velero.io/schedule-name: scheduleName and waits for it to finish.
func WaitForScheduleCompletion(testCtx *internal.TestContext, scheduleName string) error {
	// Find the most recent backup created by this schedule
	backupName, err := getLatestBackupForSchedule(testCtx.Context, testCtx.MgmtClient, DefaultOADPNamespace, scheduleName)
	if err != nil {
		return fmt.Errorf("failed to find backup for schedule %s: %w", scheduleName, err)
	}

	// Wait for backup to reach a final state
	checkFn := isBackupInFinalState(testCtx.MgmtClient, DefaultOADPNamespace, backupName)
	if err := wait.PollUntilContextTimeout(testCtx.Context, PollInterval, BackupTimeout, true, func(ctx context.Context) (bool, error) {
		return checkFn(ctx)
	}); err != nil {
		return fmt.Errorf("backup %s (from schedule %s) did not reach final state within %v: %w", backupName, scheduleName, BackupTimeout, err)
	}

	return ensureBackupSuccessful(testCtx.Context, testCtx.MgmtClient, DefaultOADPNamespace, backupName)
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

// isVeleroResourceInFinalState returns a function that checks if a Velero resource (Backup or Restore)
// is in a final state. This can be both success and failure.
func isVeleroResourceInFinalState(
	client crclient.Client,
	namespace, name, kind string,
	phasesNotDone []string,
	getResourceFunc func(context.Context, crclient.Client, string, string) (*unstructured.Unstructured, error),
) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		resource, err := getResourceFunc(ctx, client, namespace, name)
		if err != nil {
			return false, fmt.Errorf("failed to get %s %s: %w", kind, name, err)
		}

		phase, found, err := unstructured.NestedString(resource.Object, "status", "phase")
		if err != nil {
			return false, fmt.Errorf("failed to get %s phase: %w", kind, err)
		}

		if !found {
			// This is expected temporarily
			return false, nil
		}

		// Check if phase is in the "not done" list
		for _, notDonePhase := range phasesNotDone {
			if phase == notDonePhase {
				return false, nil
			}
		}
		return true, nil
	}
}

// isBackupInFinalState returns a function that checks if a backup is in a final state. This
// can be both success and failure.
func isBackupInFinalState(client crclient.Client, namespace, name string) func(context.Context) (bool, error) {
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
	return isVeleroResourceInFinalState(client, namespace, name, "backup", phasesNotDone, getBackup)
}

// WaitForRestoreCompletion waits for a restore to complete.
// If restoreName is provided, it waits for that specific restore.
// If restoreName is empty, it finds the most recent restore matching the HostedCluster name/namespace.
func WaitForRestoreCompletion(testCtx *internal.TestContext, restoreName string) error {
	// If no restore name provided, find the most recent restore matching the prefix
	if restoreName == "" {
		var err error
		restoreName, err = getLatestRestoreForHostedCluster(testCtx.Context, testCtx.MgmtClient, DefaultOADPNamespace, testCtx.ClusterName, testCtx.ClusterNamespace)
		if err != nil {
			return fmt.Errorf("failed to find restore for HostedCluster %s/%s: %w", testCtx.ClusterNamespace, testCtx.ClusterName, err)
		}
	}

	// Wait for restore to reach a final state
	checkFn := isRestoreInFinalState(testCtx.MgmtClient, DefaultOADPNamespace, restoreName)
	if err := wait.PollUntilContextTimeout(testCtx.Context, 10*time.Second, RestoreTimeout, true, func(ctx context.Context) (bool, error) {
		return checkFn(ctx)
	}); err != nil {
		return fmt.Errorf("restore %s did not reach final state within %v: %w", restoreName, RestoreTimeout, err)
	}

	return ensureRestoreSuccessful(testCtx.Context, testCtx.MgmtClient, DefaultOADPNamespace, restoreName)
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
	return getLatestVeleroResourceForHostedCluster(ctx, client, oadpNamespace, hcName, hcNamespace, "Restore", "RestoreList")
}

// getRestore retrieves a restore by name
func getRestore(ctx context.Context, client crclient.Client, namespace, name string) (*unstructured.Unstructured, error) {
	return getVeleroResource(ctx, client, namespace, name, "Restore")
}

// isRestoreInFinalState returns a function that checks if a restore is in a final state.
// This can be both success and failure.
func isRestoreInFinalState(client crclient.Client, namespace, name string) func(context.Context) (bool, error) {
	phasesNotDone := []string{
		RestorePhaseNew,
		RestorePhaseInProgress,
		RestorePhaseWaitingForPluginOperations,
		RestorePhaseWaitingForPluginOperationsPartiallyFailed,
		RestorePhaseFinalizing,
		RestorePhaseFinalizingPartiallyFailed,
	}
	return isVeleroResourceInFinalState(client, namespace, name, "restore", phasesNotDone, getRestore)
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
		Namespace: DefaultOADPNamespace,
		Name:      scheduleName,
	}, schedule)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already deleted
			return nil
		}
		return fmt.Errorf("failed to get Schedule %s/%s: %w", DefaultOADPNamespace, scheduleName, err)
	}

	// Delete the Schedule
	if err := testCtx.MgmtClient.Delete(testCtx.Context, schedule); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete Schedule %s/%s: %w", DefaultOADPNamespace, scheduleName, err)
		}
	}

	return nil
}
