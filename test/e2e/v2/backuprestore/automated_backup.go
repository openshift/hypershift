//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	AutomatedBackupCronJobName      = "etcd-backup-gcs"
	AutomatedBackupServiceAccount   = "etcd-backup-gcs"
	AutomatedBackupRoleName         = "etcd-backup-gcs"
	AutomatedBackupRoleBindingName  = "etcd-backup-gcs"
	AutomatedBackupAlertingRuleName = "etcd-backup-alerting-rules"

	AutomatedBackupTimeout = 10 * time.Minute
)

// ValidateCronJobSpec fetches the etcd-backup-gcs CronJob and validates its spec
// against the expected configuration from the component at etcd_backup_gcs/cronjob.go.
func ValidateCronJobSpec(testCtx *internal.TestContext, namespace, expectedSchedule string) error {
	cronJob := &batchv1.CronJob{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: namespace,
		Name:      AutomatedBackupCronJobName,
	}, cronJob); err != nil {
		return fmt.Errorf("failed to get CronJob %s: %w", AutomatedBackupCronJobName, err)
	}

	if expectedSchedule != "" && cronJob.Spec.Schedule != expectedSchedule {
		return fmt.Errorf("CronJob schedule: got %q, want %q", cronJob.Spec.Schedule, expectedSchedule)
	}
	if cronJob.Spec.Schedule == "" {
		return fmt.Errorf("CronJob schedule is empty")
	}

	if cronJob.Spec.ConcurrencyPolicy != batchv1.ForbidConcurrent {
		return fmt.Errorf("CronJob concurrencyPolicy: got %q, want %q",
			cronJob.Spec.ConcurrencyPolicy, batchv1.ForbidConcurrent)
	}

	if cronJob.Spec.SuccessfulJobsHistoryLimit == nil || *cronJob.Spec.SuccessfulJobsHistoryLimit != 3 {
		return fmt.Errorf("CronJob successfulJobsHistoryLimit: got %v, want 3",
			cronJob.Spec.SuccessfulJobsHistoryLimit)
	}

	if cronJob.Spec.FailedJobsHistoryLimit == nil || *cronJob.Spec.FailedJobsHistoryLimit != 3 {
		return fmt.Errorf("CronJob failedJobsHistoryLimit: got %v, want 3",
			cronJob.Spec.FailedJobsHistoryLimit)
	}

	podSpec := cronJob.Spec.JobTemplate.Spec.Template.Spec

	if podSpec.RestartPolicy != corev1.RestartPolicyNever {
		return fmt.Errorf("CronJob restartPolicy: got %q, want %q",
			podSpec.RestartPolicy, corev1.RestartPolicyNever)
	}

	if podSpec.ServiceAccountName != AutomatedBackupServiceAccount {
		return fmt.Errorf("CronJob serviceAccountName: got %q, want %q",
			podSpec.ServiceAccountName, AutomatedBackupServiceAccount)
	}

	if len(podSpec.InitContainers) == 0 {
		return fmt.Errorf("CronJob has no init containers, expected 'snapshot' container")
	}
	snapshotContainer := podSpec.InitContainers[0]
	if snapshotContainer.Name != "snapshot" {
		return fmt.Errorf("CronJob init container name: got %q, want %q",
			snapshotContainer.Name, "snapshot")
	}

	if len(podSpec.Containers) == 0 {
		return fmt.Errorf("CronJob has no containers, expected 'upload' container")
	}
	uploadContainer := podSpec.Containers[0]
	if uploadContainer.Name != "upload" {
		return fmt.Errorf("CronJob main container name: got %q, want %q",
			uploadContainer.Name, "upload")
	}

	requiredVolumes := []string{"client-tls", "etcd-ca", "etcd-snapshot", "root-ca", "etcd-signer", "sa-signing-key"}
	volumeNames := make(map[string]bool)
	for _, v := range podSpec.Volumes {
		volumeNames[v.Name] = true
	}
	for _, required := range requiredVolumes {
		if !volumeNames[required] {
			return fmt.Errorf("CronJob missing required volume %q", required)
		}
	}

	return nil
}

// ValidateBackupRBACResources verifies the ServiceAccount, Role, and RoleBinding
// created by the etcd_backup_gcs component for the backup CronJob.
func ValidateBackupRBACResources(testCtx *internal.TestContext, namespace, gcpServiceAccountEmail string) error {
	sa := &corev1.ServiceAccount{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: namespace,
		Name:      AutomatedBackupServiceAccount,
	}, sa); err != nil {
		return fmt.Errorf("failed to get ServiceAccount %s: %w", AutomatedBackupServiceAccount, err)
	}

	wiAnnotation := sa.Annotations["iam.gke.io/gcp-service-account"]
	if wiAnnotation != gcpServiceAccountEmail {
		return fmt.Errorf("ServiceAccount Workload Identity annotation: got %q, want %q",
			wiAnnotation, gcpServiceAccountEmail)
	}

	role := &rbacv1.Role{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: namespace,
		Name:      AutomatedBackupRoleName,
	}, role); err != nil {
		return fmt.Errorf("failed to get Role %s: %w", AutomatedBackupRoleName, err)
	}

	if len(role.Rules) == 0 {
		return fmt.Errorf("Role %s has no rules", AutomatedBackupRoleName)
	}

	rb := &rbacv1.RoleBinding{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: namespace,
		Name:      AutomatedBackupRoleBindingName,
	}, rb); err != nil {
		return fmt.Errorf("failed to get RoleBinding %s: %w", AutomatedBackupRoleBindingName, err)
	}

	if rb.RoleRef.Name != AutomatedBackupRoleName {
		return fmt.Errorf("RoleBinding roleRef.name: got %q, want %q",
			rb.RoleRef.Name, AutomatedBackupRoleName)
	}

	return nil
}

// ValidateAlertingRules verifies that the PrometheusRule for etcd backup alerts
// exists and contains the expected alert rules.
func ValidateAlertingRules(testCtx *internal.TestContext, namespace string) error {
	rule := &monitoringv1.PrometheusRule{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: namespace,
		Name:      AutomatedBackupAlertingRuleName,
	}, rule); err != nil {
		return fmt.Errorf("failed to get PrometheusRule %s: %w", AutomatedBackupAlertingRuleName, err)
	}

	expectedAlerts := map[string]bool{
		"EtcdBackupStale":  false,
		"EtcdRestoreFailed": false,
	}

	for _, group := range rule.Spec.Groups {
		for _, r := range group.Rules {
			if _, ok := expectedAlerts[r.Alert]; ok {
				expectedAlerts[r.Alert] = true
			}
		}
	}

	for alertName, found := range expectedAlerts {
		if !found {
			return fmt.Errorf("PrometheusRule %s missing expected alert %q", AutomatedBackupAlertingRuleName, alertName)
		}
	}

	return nil
}

// TriggerCronJobManually creates a Job from the CronJob's jobTemplate spec.
// This avoids waiting for the CronJob schedule to fire (which defaults to hourly).
func TriggerCronJobManually(testCtx *internal.TestContext, namespace string) (string, error) {
	cronJob := &batchv1.CronJob{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Namespace: namespace,
		Name:      AutomatedBackupCronJobName,
	}, cronJob); err != nil {
		return "", fmt.Errorf("failed to get CronJob %s: %w", AutomatedBackupCronJobName, err)
	}

	jobName := fmt.Sprintf("%s-manual-%d", AutomatedBackupCronJobName, time.Now().Unix())
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels:    cronJob.Spec.JobTemplate.Labels,
		},
		Spec: *cronJob.Spec.JobTemplate.Spec.DeepCopy(),
	}

	if err := testCtx.MgmtClient.Create(testCtx.Context, job); err != nil {
		return "", fmt.Errorf("failed to create manual backup Job: %w", err)
	}

	return jobName, nil
}

// WaitForJobCompletion polls until the named Job reaches Complete or Failed status.
// On failure, it returns an error with diagnostic information from the Job's pods.
func WaitForJobCompletion(testCtx *internal.TestContext, namespace, jobName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		job := &batchv1.Job{}
		if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{
			Namespace: namespace,
			Name:      jobName,
		}, job); err != nil {
			return false, fmt.Errorf("failed to get Job %s: %w", jobName, err)
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				msg := getJobPodTerminationMessages(ctx, testCtx.MgmtClient, namespace, jobName)
				return false, fmt.Errorf("Job %s failed: %s; pod messages: %s", jobName, cond.Message, msg)
			}
		}
		return false, nil
	})
}

func getJobPodTerminationMessages(ctx context.Context, client crclient.Client, namespace, jobName string) string {
	podList := &corev1.PodList{}
	if err := client.List(ctx, podList, &crclient.ListOptions{
		Namespace:     namespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{"job-name": jobName}),
	}); err != nil {
		return fmt.Sprintf("(failed to list pods: %v)", err)
	}

	var messages []string
	for _, pod := range podList.Items {
		for _, cs := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				messages = append(messages, fmt.Sprintf("%s/%s: %s", pod.Name, cs.Name, cs.State.Terminated.Message))
			}
		}
	}
	if len(messages) == 0 {
		return "(no termination messages)"
	}
	return strings.Join(messages, "; ")
}

// WaitForEtcdBackupSucceededCondition polls the HostedCluster for the
// EtcdBackupSucceeded condition with the expected status.
func WaitForEtcdBackupSucceededCondition(testCtx *internal.TestContext, expectedStatus metav1.ConditionStatus) error {
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, AutomatedBackupTimeout, true, func(ctx context.Context) (bool, error) {
		hc := &hyperv1.HostedCluster{}
		if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{
			Name:      testCtx.ClusterName,
			Namespace: testCtx.ClusterNamespace,
		}, hc); err != nil {
			return false, fmt.Errorf("failed to get HostedCluster: %w", err)
		}

		condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))
		if condition == nil {
			return false, nil
		}
		return condition.Status == expectedStatus, nil
	})
}

// VerifyGCSBackupExists checks that at least one .tar.gz backup archive exists
// in GCS at the expected path: gs://{bucket}/{infraID}/.
func VerifyGCSBackupExists(testCtx *internal.TestContext, bucket, infraID string) error {
	gcloudPath, err := exec.LookPath("gcloud")
	if err != nil {
		return fmt.Errorf("gcloud CLI not found in PATH: %w", err)
	}

	cmd := exec.CommandContext(testCtx.Context,
		gcloudPath, "storage", "ls", fmt.Sprintf("gs://%s/%s/", bucket, infraID))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gcloud storage ls failed: %w\noutput: %s", err, output)
	}
	if !strings.Contains(string(output), ".tar.gz") {
		return fmt.Errorf("no .tar.gz archives found at gs://%s/%s/\noutput: %s", bucket, infraID, output)
	}
	return nil
}
