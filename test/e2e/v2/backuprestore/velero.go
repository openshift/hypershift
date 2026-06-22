//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"fmt"
	"log"
	"slices"
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

// DPAPluginState captures the original DPA state so that cleanup can restore
// the defaultPlugins list after a test run.
type DPAPluginState struct {
	// Name is the name of the DPA that was modified.
	Name string
	// OriginalPlugins is the defaultPlugins list before modification.
	OriginalPlugins []string
	// PluginsModified indicates whether the DPA was actually updated.
	PluginsModified bool
}

var dpaGVK = schema.GroupVersionKind{
	Group:   "oadp.openshift.io",
	Version: "v1alpha1",
	Kind:    "DataProtectionApplicationList",
}

// EnsureDPAHypershiftPlugin ensures the first DataProtectionApplication in
// DefaultOADPNamespace has the hypershift plugin configured. The plugin can be
// present either in spec.configuration.velero.defaultPlugins (as "hypershift")
// or in spec.configuration.velero.customPlugins (as an entry named
// "hypershift-oadp-plugin"). If the plugin is already present via either
// mechanism this is a no-op. When the plugin is appended to defaultPlugins
// the function waits for the Velero pod to restart and become ready.
func EnsureDPAHypershiftPlugin(testCtx *internal.TestContext) (*DPAPluginState, error) {
	client := testCtx.MgmtClient
	ctx := testCtx.Context

	dpaList := &unstructured.UnstructuredList{}
	dpaList.SetGroupVersionKind(dpaGVK)
	if err := client.List(ctx, dpaList, crclient.InNamespace(DefaultOADPNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list DataProtectionApplication resources: %w", err)
	}
	if len(dpaList.Items) == 0 {
		return nil, fmt.Errorf("no DataProtectionApplication resources found in namespace %s", DefaultOADPNamespace)
	}

	dpa := dpaList.Items[0]
	plugins, _, err := unstructured.NestedStringSlice(dpa.Object, "spec", "configuration", "velero", "defaultPlugins")
	if err != nil {
		return nil, fmt.Errorf("failed to read defaultPlugins from DPA %s: %w", dpa.GetName(), err)
	}

	state := &DPAPluginState{
		Name:            dpa.GetName(),
		OriginalPlugins: plugins,
	}

	if slices.Contains(plugins, "hypershift") {
		return state, nil
	}

	// Check if the plugin is already configured via customPlugins.
	// Adding "hypershift" to defaultPlugins when it already exists in
	// customPlugins causes the OADP operator to generate duplicate
	// init containers named "hypershift-oadp-plugin" in the velero
	// Deployment, which fails Kubernetes validation.
	if hasHypershiftCustomPlugin(&dpa) {
		return state, nil
	}

	// Append the hypershift plugin and update.
	updatedPlugins := append(plugins, "hypershift")
	if err := unstructured.SetNestedStringSlice(dpa.Object, updatedPlugins, "spec", "configuration", "velero", "defaultPlugins"); err != nil {
		return nil, fmt.Errorf("failed to set defaultPlugins on DPA %s: %w", dpa.GetName(), err)
	}
	if err := client.Update(ctx, &dpa); err != nil {
		return nil, fmt.Errorf("failed to update DPA %s with hypershift plugin: %w", dpa.GetName(), err)
	}
	state.PluginsModified = true

	// Wait for Velero to restart and DPA to reconcile with the new plugin.
	// Use immediate=false so the first check happens after the poll interval,
	// giving the OADP controller time to process the spec change. With
	// immediate=true the stale Reconciled=True status from the previous
	// reconciliation satisfies the check before the controller reacts.
	const dpaReconcileTimeout = 10 * time.Minute
	var lastVeleroErr, lastDPAErr error
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Second, dpaReconcileTimeout, false, func(ctx context.Context) (bool, error) {
		lastVeleroErr = EnsureVeleroPodRunning(testCtx)
		if lastVeleroErr != nil {
			log.Printf("Velero pod not ready: %v", lastVeleroErr)
			return false, nil
		}
		lastDPAErr = ensureDPAReconciled(ctx, client, DefaultOADPNamespace)
		if lastDPAErr != nil {
			log.Printf("DPA not reconciled: %v", lastDPAErr)
			return false, nil
		}
		return true, nil
	}); err != nil {
		// Collect final diagnostic state to aid debugging.
		diag := collectDPADiagnostics(ctx, testCtx, client)
		return state, fmt.Errorf("velero pod or DPA did not become ready within %v after adding hypershift plugin to DPA %s: %w\nlast velero pod check: %v\nlast DPA reconciled check: %v\n%s",
			dpaReconcileTimeout, dpa.GetName(), err, lastVeleroErr, lastDPAErr, diag)
	}

	return state, nil
}

// collectDPADiagnostics gathers Velero pod statuses and DPA conditions for
// inclusion in timeout error messages.
func collectDPADiagnostics(ctx context.Context, testCtx *internal.TestContext, client crclient.Client) string {
	var b strings.Builder

	// Velero pod status.
	podList := &corev1.PodList{}
	labels := map[string]string{
		"deploy":    "velero",
		"component": "velero",
	}
	if err := client.List(ctx, podList, crclient.InNamespace(DefaultOADPNamespace), crclient.MatchingLabels(labels)); err != nil {
		fmt.Fprintf(&b, "diagnostics: failed to list Velero pods: %v\n", err)
	} else if len(podList.Items) == 0 {
		fmt.Fprintf(&b, "diagnostics: no Velero pods found in namespace %s\n", DefaultOADPNamespace)
	} else {
		for _, pod := range podList.Items {
			fmt.Fprintf(&b, "diagnostics: velero pod %s: phase=%s", pod.Name, pod.Status.Phase)
			for _, cs := range pod.Status.ContainerStatuses {
				fmt.Fprintf(&b, " container=%s ready=%t restarts=%d", cs.Name, cs.Ready, cs.RestartCount)
				if cs.State.Waiting != nil {
					fmt.Fprintf(&b, " waiting=%s(%s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
				if cs.State.Terminated != nil {
					fmt.Fprintf(&b, " terminated=%s(exit=%d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
				}
			}
			fmt.Fprintln(&b)
		}
	}

	// DPA conditions.
	dpaList := &unstructured.UnstructuredList{}
	dpaList.SetGroupVersionKind(dpaGVK)
	if err := client.List(ctx, dpaList, crclient.InNamespace(DefaultOADPNamespace)); err != nil {
		fmt.Fprintf(&b, "diagnostics: failed to list DPA resources: %v\n", err)
	} else {
		for _, dpa := range dpaList.Items {
			conditions, found, err := unstructured.NestedSlice(dpa.Object, "status", "conditions")
			if err != nil || !found {
				fmt.Fprintf(&b, "diagnostics: DPA %s: no conditions found\n", dpa.GetName())
				continue
			}
			fmt.Fprintf(&b, "diagnostics: DPA %s conditions:", dpa.GetName())
			for _, condIface := range conditions {
				cond, ok := condIface.(map[string]interface{})
				if !ok {
					continue
				}
				condType, _ := cond["type"].(string)
				condStatus, _ := cond["status"].(string)
				condMsg, _ := cond["message"].(string)
				condReason, _ := cond["reason"].(string)
				fmt.Fprintf(&b, " [%s=%s reason=%q message=%q]", condType, condStatus, condReason, condMsg)
			}
			fmt.Fprintln(&b)
		}
	}

	return b.String()
}

// ensureDPAReconciled checks that at least one DPA in the namespace has
// the Reconciled condition set to True.
func ensureDPAReconciled(ctx context.Context, c crclient.Client, namespace string) error {
	dpaList := &unstructured.UnstructuredList{}
	dpaList.SetGroupVersionKind(dpaGVK)
	if err := c.List(ctx, dpaList, crclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list DPA resources: %w", err)
	}
	for _, dpa := range dpaList.Items {
		conditions, found, err := unstructured.NestedSlice(dpa.Object, "status", "conditions")
		if err != nil || !found {
			continue
		}
		for _, condIface := range conditions {
			cond, ok := condIface.(map[string]interface{})
			if !ok {
				continue
			}
			if condType, _ := cond["type"].(string); condType == "Reconciled" {
				if condStatus, _ := cond["status"].(string); condStatus == "True" {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("no reconciled DPA found in namespace %s", namespace)
}

// hasHypershiftCustomPlugin checks whether the DPA has an entry named
// "hypershift-oadp-plugin" in spec.configuration.velero.customPlugins.
func hasHypershiftCustomPlugin(dpa *unstructured.Unstructured) bool {
	customPlugins, found, err := unstructured.NestedSlice(dpa.Object, "spec", "configuration", "velero", "customPlugins")
	if err != nil || !found {
		return false
	}
	for _, p := range customPlugins {
		plugin, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := plugin["name"].(string); name == "hypershift-oadp-plugin" {
			return true
		}
	}
	return false
}

// RestoreDPAPlugins restores the original defaultPlugins list on the DPA.
// If the DPA was not modified this is a no-op.
func RestoreDPAPlugins(testCtx *internal.TestContext, state *DPAPluginState) error {
	if !state.PluginsModified {
		return nil
	}

	client := testCtx.MgmtClient
	ctx := testCtx.Context

	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	if err := client.Get(ctx, crclient.ObjectKey{
		Namespace: DefaultOADPNamespace,
		Name:      state.Name,
	}, dpa); err != nil {
		return fmt.Errorf("failed to get DPA %s for plugin restore: %w", state.Name, err)
	}

	if err := unstructured.SetNestedStringSlice(dpa.Object, state.OriginalPlugins, "spec", "configuration", "velero", "defaultPlugins"); err != nil {
		return fmt.Errorf("failed to set original defaultPlugins on DPA %s: %w", state.Name, err)
	}
	if err := client.Update(ctx, dpa); err != nil {
		return fmt.Errorf("failed to restore DPA %s plugins: %w", state.Name, err)
	}

	return nil
}
