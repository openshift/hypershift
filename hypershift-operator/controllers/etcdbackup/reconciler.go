package etcdbackup

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	supportconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "hcpetcdbackup"

	// Labels used on backup Jobs.
	LabelApp  = "app"
	LabelName = "etcd-backup"
	labelHCP  = "hypershift.openshift.io/hcp"

	// LabelBackupName is the label key for the backup CR name on Jobs.
	LabelBackupName = "hypershift.openshift.io/backup-name"
	// LabelHCPNamespace is the label key for the HCP namespace on Jobs.
	LabelHCPNamespace = "hypershift.openshift.io/hcp-namespace"

	// pullSecretName is the name of the pull secret copied to HCP namespaces.
	pullSecretName = "pull-secret"

	// RBACName is the name of the Role and RoleBinding created in HCP namespaces.
	RBACName = "etcd-backup-job"
	// NetworkPolicyName is the name of the NetworkPolicy created in HCP namespaces.
	NetworkPolicyName = "allow-etcd-backup"

	// ServiceAccount name for backup Jobs in the HO namespace.
	jobServiceAccountName = "etcd-backup-job"

	// Volume names.
	volumeEtcdCerts   = "etcd-certs"
	volumeEtcdBackup  = "etcd-backup"
	volumeCredentials = "backup-credentials"

	// Mount paths.
	mountPathEtcdCerts   = "/etc/etcd-certs"
	mountPathEtcdBackup  = "/etc/etcd-backup"
	mountPathCredentials = "/etc/etcd-backup-creds"

	requeueInterval = 10 * time.Second
)

// HCPEtcdBackupReconciler reconciles HCPEtcdBackup resources by orchestrating
// etcd snapshot and upload Jobs in the HyperShift Operator namespace.
type HCPEtcdBackupReconciler struct {
	client.Client
	OperatorNamespace       string
	ReleaseProvider         releaseinfo.ProviderWithOpenShiftImageRegistryOverrides
	HypershiftOperatorImage string
	MaxBackupCount          int
}

func (r *HCPEtcdBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&hyperv1.HCPEtcdBackup{}).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				backupName := obj.GetLabels()[LabelBackupName]
				hcpNamespace := obj.GetLabels()[LabelHCPNamespace]
				if backupName == "" || hcpNamespace == "" {
					return nil
				}
				return []reconcile.Request{{
					NamespacedName: types.NamespacedName{
						Name:      backupName,
						Namespace: hcpNamespace,
					},
				}}
			},
		)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 30*time.Second),
		}).
		Complete(r)
}

func (r *HCPEtcdBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Feature gate check
	if !featuregate.Gate().Enabled(featuregate.HCPEtcdBackup) {
		return ctrl.Result{}, nil
	}

	// Fetch the HCPEtcdBackup CR
	backup := &hyperv1.HCPEtcdBackup{}
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get HCPEtcdBackup: %w", err)
	}

	// If backup is in a terminal state, ensure cleanup and run retention.
	// Return errors so controller-runtime retries cleanup on transient failures,
	// preventing leaked RBAC or NetworkPolicy resources.
	if isTerminal(backup) {
		if err := r.cleanupResources(ctx, backup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to cleanup resources for completed backup: %w", err)
		}
		if err := r.enforceRetention(ctx, backup.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to enforce retention: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Look up the HostedControlPlane in the same namespace
	hcp, err := r.getHostedControlPlane(ctx, backup.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up HostedControlPlane: %w", err)
	}
	if hcp == nil {
		r.setCondition(backup, metav1.Condition{
			Type:    string(hyperv1.BackupCompleted),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.BackupFailedReason,
			Message: "HostedControlPlane not found in namespace " + backup.Namespace,
		})
		if err := r.Status().Update(ctx, backup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Phase 1 health check: etcd StatefulSet readiness
	healthy, msg, err := r.checkEtcdHealth(ctx, backup.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check etcd health: %w", err)
	}
	if !healthy {
		r.setCondition(backup, metav1.Condition{
			Type:    string(hyperv1.BackupCompleted),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.EtcdUnhealthyReason,
			Message: msg,
		})
		if err := r.Status().Update(ctx, backup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Check if we already created a Job for this backup
	existingJob, err := r.findJobForBackup(ctx, backup)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to find job for backup: %w", err)
	}

	if existingJob != nil {
		// Monitor existing Job status
		return r.handleJobStatus(ctx, backup, existingJob, hcp)
	}

	// Serial execution guard: reject if another backup's Job is already active.
	// This runs after findJobForBackup so we don't reject our own Job.
	activeJob, err := r.findActiveJob(ctx, backup.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for active jobs: %w", err)
	}
	if activeJob != nil {
		logger.Info("rejecting backup: another backup Job is already active", "activeJob", activeJob.Name)
		r.setCondition(backup, metav1.Condition{
			Type:    string(hyperv1.BackupCompleted),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.BackupRejectedReason,
			Message: fmt.Sprintf("rejected: backup Job %q is already running for this HCP; delete this CR and retry after the active backup completes", activeJob.Name),
		})
		if err := r.Status().Update(ctx, backup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Validate prerequisites before creating any resources.
	// Check credential Secret early so we don't create RBAC/NetworkPolicy unnecessarily.
	credentialSecretName, err := r.getCredentialSecretName(backup)
	if err != nil {
		r.setCondition(backup, metav1.Condition{
			Type:    string(hyperv1.BackupCompleted),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.BackupFailedReason,
			Message: err.Error(),
		})
		if statusErr := r.Status().Update(ctx, backup); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", statusErr)
		}
		return ctrl.Result{}, nil
	}

	credSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: credentialSecretName, Namespace: r.OperatorNamespace}, credSecret); err != nil {
		if apierrors.IsNotFound(err) {
			r.setCondition(backup, metav1.Condition{
				Type:    string(hyperv1.BackupCompleted),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.BackupFailedReason,
				Message: fmt.Sprintf("credential Secret %q not found in namespace %q", credentialSecretName, r.OperatorNamespace),
			})
			if statusErr := r.Status().Update(ctx, backup); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", statusErr)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get credential Secret: %w", err)
	}

	// Create resources and Job
	logger.Info("creating backup resources", "backup", backup.Name, "namespace", backup.Namespace)

	if err := r.ensureServiceAccount(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure ServiceAccount: %w", err)
	}

	if err := r.ensureRBAC(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure RBAC: %w", err)
	}

	if err := r.ensureNetworkPolicy(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure NetworkPolicy: %w", err)
	}

	if err := r.createBackupJob(ctx, backup, hcp); err != nil {
		if apierrors.IsNotFound(err) {
			r.setCondition(backup, metav1.Condition{
				Type:    string(hyperv1.BackupCompleted),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.BackupFailedReason,
				Message: err.Error(),
			})
			if statusErr := r.Status().Update(ctx, backup); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", statusErr)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to create backup Job: %w", err)
	}

	// Set status to indicate backup is in progress
	r.setCondition(backup, metav1.Condition{
		Type:    string(hyperv1.BackupCompleted),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.BackupInProgressReason,
		Message: "Backup Job created, waiting for completion",
	})
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	// Bubble up to HCP
	if err := r.updateHCPBackupCondition(ctx, hcp, metav1.Condition{
		Type:    string(hyperv1.EtcdBackupSucceeded),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.BackupInProgressReason,
		Message: fmt.Sprintf("Backup %q is in progress", backup.Name),
	}); err != nil {
		logger.Error(err, "failed to update HCP backup condition")
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// isTerminal returns true if the backup is in a terminal state.
func isTerminal(backup *hyperv1.HCPEtcdBackup) bool {
	cond := meta.FindStatusCondition(backup.Status.Conditions, string(hyperv1.BackupCompleted))
	if cond == nil {
		return false
	}
	return cond.Status == metav1.ConditionTrue ||
		cond.Reason == hyperv1.BackupFailedReason ||
		cond.Reason == hyperv1.BackupRejectedReason
}

// setCondition sets or updates a condition on the backup status.
func (r *HCPEtcdBackupReconciler) setCondition(backup *hyperv1.HCPEtcdBackup, condition metav1.Condition) {
	condition.ObservedGeneration = backup.Generation
	meta.SetStatusCondition(&backup.Status.Conditions, condition)
}

// updateHCPBackupCondition sets a condition on the HostedControlPlane to bubble
// up the etcd backup status. The HC controller propagates this to the HostedCluster.
func (r *HCPEtcdBackupReconciler) updateHCPBackupCondition(ctx context.Context, hcp *hyperv1.HostedControlPlane, condition metav1.Condition) error {
	condition.ObservedGeneration = hcp.Generation
	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
	return r.Status().Update(ctx, hcp)
}

// updateHostedClusterBackupURL persists the snapshot URL in the HostedCluster
// status so it survives HCPEtcdBackup CR retention/deletion.
// Uses RetryOnConflict because the HC is updated by multiple controllers,
// and a requeue-based retry risks losing the URL if the Pod is cleaned up
// (TTLSecondsAfterFinished) before the next reconcile extracts it.
func (r *HCPEtcdBackupReconciler) updateHostedClusterBackupURL(ctx context.Context, hcp *hyperv1.HostedControlPlane, snapshotURL string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		hc, err := k8sutil.HostedClusterFromAnnotation(ctx, r.Client, hcp)
		if err != nil {
			return err
		}
		hc.Status.LastSuccessfulEtcdBackupURL = snapshotURL
		return r.Status().Update(ctx, hc)
	})
}

// getHostedControlPlane finds the HostedControlPlane in the given namespace.
// Returns nil if none found.
func (r *HCPEtcdBackupReconciler) getHostedControlPlane(ctx context.Context, namespace string) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if len(hcpList.Items) == 0 {
		return nil, nil
	}
	return &hcpList.Items[0], nil
}

// checkEtcdHealth verifies the etcd StatefulSet has all replicas ready.
func (r *HCPEtcdBackupReconciler) checkEtcdHealth(ctx context.Context, namespace string) (bool, string, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: "etcd", Namespace: namespace}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return false, "etcd StatefulSet not found", nil
		}
		return false, "", err
	}

	desired := ptr.Deref(sts.Spec.Replicas, 1)
	if sts.Status.ReadyReplicas < desired {
		return false, fmt.Sprintf("etcd StatefulSet not fully ready: %d/%d replicas ready",
			sts.Status.ReadyReplicas, desired), nil
	}
	return true, "", nil
}

// findActiveJob checks if any backup Job is currently active for the given HCP namespace.
// Callers must check for their own backup's Job first (via findJobForBackup) to avoid
// self-rejection when re-reconciling after Job creation.
func (r *HCPEtcdBackupReconciler) findActiveJob(ctx context.Context, hcpNamespace string) (*batchv1.Job, error) {
	jobList := &batchv1.JobList{}
	if err := r.List(ctx, jobList,
		client.InNamespace(r.OperatorNamespace),
		client.MatchingLabels{
			LabelApp:          LabelName,
			LabelHCPNamespace: hcpNamespace,
		},
	); err != nil {
		return nil, err
	}

	for i := range jobList.Items {
		job := &jobList.Items[i]
		if job.Status.Active > 0 {
			return job, nil
		}
	}
	return nil, nil
}

// findJobForBackup finds the Job created for this specific backup.
func (r *HCPEtcdBackupReconciler) findJobForBackup(ctx context.Context, backup *hyperv1.HCPEtcdBackup) (*batchv1.Job, error) {
	jobList := &batchv1.JobList{}
	if err := r.List(ctx, jobList,
		client.InNamespace(r.OperatorNamespace),
		client.MatchingLabels{
			LabelBackupName:   backup.Name,
			LabelHCPNamespace: backup.Namespace,
		},
	); err != nil {
		return nil, err
	}
	if len(jobList.Items) == 0 {
		return nil, nil
	}
	return &jobList.Items[0], nil
}

// handleJobStatus monitors Job status and updates HCPEtcdBackup conditions.
func (r *HCPEtcdBackupReconciler) handleJobStatus(ctx context.Context, backup *hyperv1.HCPEtcdBackup, job *batchv1.Job, hcp *hyperv1.HostedControlPlane) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			logger.Info("backup Job completed successfully", "job", job.Name)

			// Extract snapshotURL from the upload container's termination message.
			// The etcd-upload command writes the URL to /dev/termination-log.
			url, err := r.getSnapshotURLFromPod(ctx, job)
			if err != nil {
				logger.Error(err, "failed to read snapshot URL from pod termination message")
				url = "" // don't use url on error
			}

			// Cleanup temporary RBAC and NetworkPolicy as soon as the Job completes.
			// This must happen before any status updates that could fail and cause
			// requeue, to avoid leaving security resources exposed indefinitely.
			if err := r.cleanupResources(ctx, backup); err != nil {
				logger.Error(err, "failed to cleanup resources after successful backup")
			}

			// Persist the snapshot URL in the HostedCluster status BEFORE marking
			// the backup as terminal so the controller retries on requeue.
			// This is idempotent: if it succeeds but the backup status update below
			// fails, the next reconcile re-extracts the URL and writes the same value.
			if url != "" {
				if err := r.updateHostedClusterBackupURL(ctx, hcp, url); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update HostedCluster LastSuccessfulEtcdBackupURL: %w", err)
				}
				backup.Status.SnapshotURL = url
			}

			// Propagate encryption metadata based on storage config
			r.setEncryptionMetadata(backup)

			r.setCondition(backup, metav1.Condition{
				Type:    string(hyperv1.BackupCompleted),
				Status:  metav1.ConditionTrue,
				Reason:  hyperv1.BackupSucceededReason,
				Message: "Backup completed successfully",
			})

			if err := r.Status().Update(ctx, backup); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
			}

			// Bubble up success to HCP
			if err := r.updateHCPBackupCondition(ctx, hcp, metav1.Condition{
				Type:    string(hyperv1.EtcdBackupSucceeded),
				Status:  metav1.ConditionTrue,
				Reason:  hyperv1.BackupSucceededReason,
				Message: fmt.Sprintf("Backup %q completed successfully", backup.Name),
			}); err != nil {
				logger.Error(err, "failed to update HCP backup condition")
			}

			return ctrl.Result{}, nil
		}

		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			logger.Info("backup Job failed", "job", job.Name, "reason", cond.Message)

			// Cleanup temporary resources immediately on Job termination.
			if err := r.cleanupResources(ctx, backup); err != nil {
				logger.Error(err, "failed to cleanup resources after failed backup")
			}

			r.setCondition(backup, metav1.Condition{
				Type:    string(hyperv1.BackupCompleted),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.BackupFailedReason,
				Message: fmt.Sprintf("Backup Job failed: %s", cond.Message),
			})

			if err := r.Status().Update(ctx, backup); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
			}

			// Bubble up failure to HCP
			if err := r.updateHCPBackupCondition(ctx, hcp, metav1.Condition{
				Type:    string(hyperv1.EtcdBackupSucceeded),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.BackupFailedReason,
				Message: fmt.Sprintf("Backup %q failed: %s", backup.Name, cond.Message),
			}); err != nil {
				logger.Error(err, "failed to update HCP backup condition")
			}

			return ctrl.Result{}, nil
		}
	}

	// Job still running
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// getSnapshotURLFromPod reads the snapshot URL from the upload container's
// termination message in the Pod controlled by the given Job.
func (r *HCPEtcdBackupReconciler) getSnapshotURLFromPod(ctx context.Context, job *batchv1.Job) (string, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(job.Namespace),
		client.MatchingLabels{"batch.kubernetes.io/job-name": job.Name},
	); err != nil {
		return "", fmt.Errorf("failed to list pods for job %q: %w", job.Name, err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "upload" && cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				return strings.TrimSpace(cs.State.Terminated.Message), nil
			}
		}
	}
	return "", nil
}

// setEncryptionMetadata populates encryption metadata on the backup status
// based on the storage configuration.
func (r *HCPEtcdBackupReconciler) setEncryptionMetadata(backup *hyperv1.HCPEtcdBackup) {
	switch backup.Spec.Storage.StorageType {
	case hyperv1.S3BackupStorage:
		if backup.Spec.Storage.S3.KMSKeyARN != "" {
			backup.Status.EncryptionMetadata = hyperv1.HCPEtcdBackupEncryptionMetadata{
				AWS: hyperv1.HCPEtcdBackupEncryptionMetadataAWS{
					KMSKeyARN: backup.Spec.Storage.S3.KMSKeyARN,
				},
			}
		}
	case hyperv1.AzureBlobBackupStorage:
		if backup.Spec.Storage.AzureBlob.EncryptionKeyURL != "" {
			backup.Status.EncryptionMetadata = hyperv1.HCPEtcdBackupEncryptionMetadata{
				Azure: hyperv1.HCPEtcdBackupEncryptionMetadataAzure{
					EncryptionKeyURL: backup.Spec.Storage.AzureBlob.EncryptionKeyURL,
				},
			}
		}
	}
}

// ensureServiceAccount creates the ServiceAccount for backup Jobs in the HO namespace.
func (r *HCPEtcdBackupReconciler) ensureServiceAccount(ctx context.Context) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobServiceAccountName,
			Namespace: r.OperatorNamespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	return err
}

// ensureRBAC creates the Role and RoleBinding in the HCP namespace for the backup Job SA.
func (r *HCPEtcdBackupReconciler) ensureRBAC(ctx context.Context, backup *hyperv1.HCPEtcdBackup) error {
	// Role in HCP namespace granting read access to etcd TLS resources
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RBACName,
			Namespace: backup.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{"etcd-client-tls"},
				Verbs:         []string{"get"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{"etcd-ca"},
				Verbs:         []string{"get"},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure Role: %w", err)
	}

	// RoleBinding binding the HO namespace SA to the HCP namespace Role
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RBACName,
			Namespace: backup.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     RBACName,
		}
		rb.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      jobServiceAccountName,
				Namespace: r.OperatorNamespace,
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure RoleBinding: %w", err)
	}

	return nil
}

// ensureNetworkPolicy creates the temporary NetworkPolicy in the HCP namespace
// allowing ingress from the HO namespace to etcd on port 2379.
func (r *HCPEtcdBackupReconciler) ensureNetworkPolicy(ctx context.Context, backup *hyperv1.HCPEtcdBackup) error {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkPolicyName,
			Namespace: backup.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		etcdPort := intstr.FromInt32(supportconfig.EtcdClientPort)
		tcpProtocol := corev1.ProtocolTCP
		np.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "etcd",
				},
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": r.OperatorNamespace,
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &etcdPort,
						},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
		}
		return nil
	})
	return err
}

// cleanupResources removes temporary NetworkPolicy and RBAC from the HCP namespace.
// It skips deletion if another backup Job is still active in the same HCP namespace,
// because the shared resources (NetworkPolicy, RBAC) are needed by that Job.
func (r *HCPEtcdBackupReconciler) cleanupResources(ctx context.Context, backup *hyperv1.HCPEtcdBackup) error {
	logger := log.FromContext(ctx)

	// Guard: don't delete shared resources while another backup Job is active.
	activeJob, err := r.findActiveJob(ctx, backup.Namespace)
	if err != nil {
		return fmt.Errorf("failed to check for active jobs before cleanup: %w", err)
	}
	if activeJob != nil {
		logger.Info("skipping cleanup: another backup Job is still active", "activeJob", activeJob.Name)
		return nil
	}

	var firstErr error

	// Delete NetworkPolicy
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkPolicyName,
			Namespace: backup.Namespace,
		},
	}
	if err := r.Delete(ctx, np); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to delete NetworkPolicy", "name", NetworkPolicyName)
		firstErr = err
	}

	// Delete RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RBACName,
			Namespace: backup.Namespace,
		},
	}
	if err := r.Delete(ctx, rb); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to delete RoleBinding", "name", RBACName)
		if firstErr == nil {
			firstErr = err
		}
	}

	// Delete Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RBACName,
			Namespace: backup.Namespace,
		},
	}
	if err := r.Delete(ctx, role); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to delete Role", "name", RBACName)
		if firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// createBackupJob creates the backup Job in the HO namespace with the 3-container
// PodSpec: fetch-etcd-certs (init), etcdctl snapshot save (init), etcd-upload (main).
func (r *HCPEtcdBackupReconciler) createBackupJob(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcp *hyperv1.HostedControlPlane) error {
	// Resolve images
	pullSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: pullSecretName, Namespace: backup.Namespace}, pullSecret); err != nil {
		// Preserve error type (including IsNotFound) so caller can detect permanent failures
		return fmt.Errorf("pull secret %q in namespace %q: %w", pullSecretName, backup.Namespace, err)
	}
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]

	releaseImage := hyperutil.HCPControlPlaneReleaseImage(hcp)

	cpoImage, err := r.resolveControlPlaneOperatorImage(ctx, hcp, releaseImage, pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to resolve CPO image: %w", err)
	}

	etcdImage, err := hyperutil.GetPayloadImageFromRelease(ctx, r.ReleaseProvider, releaseImage, "etcd", pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to resolve etcd image: %w", err)
	}

	// Build upload args based on storage type
	uploadArgs, credentialSecretName, err := r.buildUploadArgs(backup)
	if err != nil {
		return fmt.Errorf("failed to build upload args: %w", err)
	}

	jobLabels := map[string]string{
		LabelApp:          LabelName,
		labelHCP:          hcp.Name,
		LabelBackupName:   backup.Name,
		LabelHCPNamespace: backup.Namespace,
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("etcd-backup-%s-", backup.Name),
			Namespace:    r.OperatorNamespace,
			Labels:       jobLabels,
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ptr.To[int32](600),
			ActiveDeadlineSeconds:   ptr.To[int64](900),
			BackoffLimit:            ptr.To[int32](0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: jobLabels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: jobServiceAccountName,
					RestartPolicy:      corev1.RestartPolicyNever,
					Volumes: []corev1.Volume{
						{
							Name: volumeEtcdCerts,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: volumeEtcdBackup,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: volumeCredentials,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: credentialSecretName,
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:  "fetch-certs",
							Image: cpoImage,
							Command: []string{
								"control-plane-operator", "fetch-etcd-certs",
								"--hcp-namespace", backup.Namespace,
								"--output-dir", mountPathEtcdCerts,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      volumeEtcdCerts,
									MountPath: mountPathEtcdCerts,
								},
							},
						},
						{
							Name:  "snapshot",
							Image: etcdImage,
							Env: []corev1.EnvVar{
								{Name: "ETCDCTL_API", Value: "3"},
							},
							Command: []string{
								"/usr/bin/etcdctl",
								"--endpoints", fmt.Sprintf("https://etcd-client.%s.svc:%d", backup.Namespace, supportconfig.EtcdClientPort),
								"--cacert", mountPathEtcdCerts + "/ca.crt",
								"--cert", mountPathEtcdCerts + "/etcd-client.crt",
								"--key", mountPathEtcdCerts + "/etcd-client.key",
								"snapshot", "save",
								mountPathEtcdBackup + "/snapshot.db",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      volumeEtcdCerts,
									MountPath: mountPathEtcdCerts,
									ReadOnly:  true,
								},
								{
									Name:      volumeEtcdBackup,
									MountPath: mountPathEtcdBackup,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "upload",
							Image:   cpoImage,
							Command: uploadArgs,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      volumeEtcdBackup,
									MountPath: mountPathEtcdBackup,
									ReadOnly:  true,
								},
								{
									Name:      volumeCredentials,
									MountPath: mountPathCredentials,
									ReadOnly:  true,
								},
							},
						},
					},
				},
			},
		},
	}

	return r.Create(ctx, job)
}

// resolveControlPlaneOperatorImage resolves the CPO image for the given HCP,
// handling annotation overrides and disconnected environments.
func (r *HCPEtcdBackupReconciler) resolveControlPlaneOperatorImage(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage string, pullSecret []byte) (string, error) {
	// Check for annotation override on HCP (propagated from HostedCluster)
	if val, ok := hcp.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation]; ok {
		return val, nil
	}

	// Resolve from release payload — the "hypershift" component is the CPO image
	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, releaseImage, pullSecret)
	if err != nil {
		return "", fmt.Errorf("failed to lookup release image: %w", err)
	}

	if hypershiftImage, exists := releaseInfo.ComponentImages()["hypershift"]; exists {
		return hypershiftImage, nil
	}

	// Fallback to HO's own image
	return r.HypershiftOperatorImage, nil
}

// getCredentialSecretName returns the name of the credential Secret referenced
// in the backup's storage configuration. This is used for early validation
// before creating RBAC/NetworkPolicy resources.
func (r *HCPEtcdBackupReconciler) getCredentialSecretName(backup *hyperv1.HCPEtcdBackup) (string, error) {
	switch backup.Spec.Storage.StorageType {
	case hyperv1.S3BackupStorage:
		return backup.Spec.Storage.S3.Credentials.Name, nil
	case hyperv1.AzureBlobBackupStorage:
		return backup.Spec.Storage.AzureBlob.Credentials.Name, nil
	}
	return "", fmt.Errorf("unsupported storage type: %s", backup.Spec.Storage.StorageType)
}

// buildUploadArgs constructs the command args for the etcd-upload container
// and returns the credential Secret name.
func (r *HCPEtcdBackupReconciler) buildUploadArgs(backup *hyperv1.HCPEtcdBackup) ([]string, string, error) {
	args := []string{
		"control-plane-operator", "etcd-upload",
		"--snapshot-path", mountPathEtcdBackup + "/snapshot.db",
	}

	switch backup.Spec.Storage.StorageType {
	case hyperv1.S3BackupStorage:
		s3 := backup.Spec.Storage.S3
		args = append(args,
			"--storage-type", "S3",
			"--aws-bucket", s3.Bucket,
			"--aws-region", s3.Region,
			"--key-prefix", s3.KeyPrefix,
			"--credentials-file", mountPathCredentials+"/credentials",
		)
		if s3.KMSKeyARN != "" {
			args = append(args, "--aws-kms-key-arn", s3.KMSKeyARN)
		}
		return args, s3.Credentials.Name, nil

	case hyperv1.AzureBlobBackupStorage:
		azure := backup.Spec.Storage.AzureBlob
		args = append(args,
			"--storage-type", "AzureBlob",
			"--azure-container", azure.Container,
			"--azure-storage-account", azure.StorageAccount,
			"--key-prefix", azure.KeyPrefix,
			"--credentials-file", mountPathCredentials+"/credentials",
		)
		if azure.EncryptionKeyURL != "" {
			args = append(args, "--azure-encryption-scope", azure.EncryptionKeyURL)
		}
		return args, azure.Credentials.Name, nil
	}

	return nil, "", fmt.Errorf("unsupported storage type: %s", backup.Spec.Storage.StorageType)
}

// enforceRetention deletes the oldest completed HCPEtcdBackup CRs if the count
// exceeds MaxBackupCount.
func (r *HCPEtcdBackupReconciler) enforceRetention(ctx context.Context, namespace string) error {
	if r.MaxBackupCount <= 0 {
		return nil
	}

	backupList := &hyperv1.HCPEtcdBackupList{}
	if err := r.List(ctx, backupList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list HCPEtcdBackup CRs: %w", err)
	}

	// Filter completed backups only
	var completed []hyperv1.HCPEtcdBackup
	for _, b := range backupList.Items {
		cond := meta.FindStatusCondition(b.Status.Conditions, string(hyperv1.BackupCompleted))
		if cond != nil && cond.Status == metav1.ConditionTrue {
			completed = append(completed, b)
		}
	}

	if len(completed) <= r.MaxBackupCount {
		return nil
	}

	// Sort by creation timestamp (oldest first)
	sort.SliceStable(completed, func(i, j int) bool {
		return completed[i].CreationTimestamp.Before(&completed[j].CreationTimestamp)
	})

	// Delete excess
	toDelete := len(completed) - r.MaxBackupCount
	for i := range toDelete {
		if err := r.Delete(ctx, &completed[i]); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete old HCPEtcdBackup %s: %w", completed[i].Name, err)
			}
		}
	}
	return nil
}
