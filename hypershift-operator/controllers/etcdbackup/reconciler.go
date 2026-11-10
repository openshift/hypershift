package etcdbackup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	supportconfig "github.com/openshift/hypershift/support/config"
	etcdutil "github.com/openshift/hypershift/support/etcd"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/releaseinfo"

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
	volumeAWSIAMToken = "aws-iam-token"

	// Mount paths.
	mountPathEtcdCerts   = "/etc/etcd-certs"
	mountPathEtcdBackup  = "/etc/etcd-backup"
	mountPathCredentials = "/etc/etcd-backup-creds"
	mountPathAWSIAMToken = "/var/run/secrets/aws-iam-token"

	requeueInterval = 10 * time.Second

	// credentialWaitRequeueInterval is the requeue delay while waiting for the
	// credential Secret to appear in the cache. This is the safety net that backs
	// up the Secret watch in case the watch event is missed.
	credentialWaitRequeueInterval = 30 * time.Second

	// credentialWaitTimeout bounds how long a backup waits for its credential
	// Secret to appear before it is marked terminally failed. Measured from the
	// backup's CreationTimestamp. Set above the OADP plugin's 30s credential
	// timeout plus cache lag and the exponential requeue backoff so the informer
	// race is absorbed, while an orphaned waiter that will never resolve is
	// eventually failed and cleaned up.
	credentialWaitTimeout = 2 * time.Minute

	// resultConfigMapKey is the ConfigMap data key holding the JSON array of
	// per-shard snapshot results. It must match etcd-upload's writer.
	resultConfigMapKey = "shardSnapshots"

	// annotationExpectedShardSnapshots records, on the backup Job, the
	// comma-separated shard names the Job was built to snapshot. The
	// completion path requires exactly one snapshot result per recorded shard.
	annotationExpectedShardSnapshots = "hypershift.openshift.io/expected-shard-snapshots"

	// annotationResultExtractionAttempts counts, on the HCPEtcdBackup, how
	// many times the completion path has tried to extract the shard snapshot
	// results from the finished Job's pod. Bounded by maxResultExtractionAttempts.
	annotationResultExtractionAttempts = "hypershift.openshift.io/result-extraction-attempts"

	// maxResultExtractionAttempts bounds retries of transient failures while
	// extracting shard snapshot results from the completed Job's pod (API
	// errors, pod status lag). Once exceeded, the backup fails terminally.
	maxResultExtractionAttempts = 5

	// resultExtractionRetryInterval is the requeue delay between shard
	// snapshot result extraction attempts.
	resultExtractionRetryInterval = 30 * time.Second

	// Backup Job deadline scaling. The single-shard S3 values preserve the
	// previous fixed 900-second deadline; each additional shard adds time for
	// its snapshot init container and upload. Azure Blob uploads get a larger
	// base and per-shard allowance. These are initial values to be validated
	// and tuned through E2E runs; the OADP orchestration timeout must be kept
	// larger than the maximum Job deadline.
	backupJobBaseDeadlineSeconds          int64 = 900
	backupJobPerShardDeadlineSeconds      int64 = 120
	azureBackupJobBaseDeadlineSeconds     int64 = 1200
	azureBackupJobPerShardDeadlineSeconds int64 = 300
)

// HCPEtcdBackupReconciler reconciles HCPEtcdBackup resources by orchestrating
// etcd snapshot and upload Jobs in the HyperShift Operator namespace.
type HCPEtcdBackupReconciler struct {
	client.Client
	// APIReader performs quorum reads that bypass the informer cache. It is used
	// for the serialization guard and own-Job lookup so a Job created moments ago
	// by another reconcile is visible immediately. Without it, the guard can
	// observe a stale cache during the create-to-cache-propagation window and
	// admit a second concurrent backup for the same HCP (check-then-create race).
	// When unset, the cached Client is used (e.g. in unit tests where the fake
	// client plays both roles).
	APIReader               client.Reader
	OperatorNamespace       string
	ReleaseProvider         releaseinfo.ProviderWithOpenShiftImageRegistryOverrides
	HypershiftOperatorImage string
	MaxBackupCount          int
}

// apiReader returns the reader used for quorum (non-cache) reads, falling back
// to the cached Client when no dedicated API reader is configured.
func (r *HCPEtcdBackupReconciler) apiReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
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
		// Watch credential Secrets in the operator namespace so a backup waiting
		// for its credential Secret is re-reconciled as soon as the Secret lands
		// in the cache, rather than only on the requeue interval.
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.backupsForCredentialSecret)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 30*time.Second),
		}).
		Complete(r)
}

func (r *HCPEtcdBackupReconciler) setFailedConditionAndUpdate(ctx context.Context, backup *hyperv1.HCPEtcdBackup, reason, message string) (ctrl.Result, error) {
	r.setCondition(backup, metav1.Condition{
		Type:    string(hyperv1.BackupCompleted),
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	return ctrl.Result{}, nil
}

// setWaitingConditionAndUpdate marks the backup as waiting (non-terminal) and
// requeues so reconciliation resumes when the awaited resource appears. This is
// used for transient preconditions like the credential Secret not yet being
// visible in the cache, which must NOT be treated as a permanent failure.
func (r *HCPEtcdBackupReconciler) setWaitingConditionAndUpdate(ctx context.Context, backup *hyperv1.HCPEtcdBackup, reason, message string) (ctrl.Result, error) {
	r.setCondition(backup, metav1.Condition{
		Type:    string(hyperv1.BackupCompleted),
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	return ctrl.Result{RequeueAfter: credentialWaitRequeueInterval}, nil
}

// handleMissingCredentialSecret marks the backup as waiting for its credential
// Secret, or, once the wait deadline (measured from CreationTimestamp) is
// exceeded, as terminally failed so shared RBAC/NetworkPolicy resources are
// cleaned up and retention can proceed instead of the CR being orphaned.
func (r *HCPEtcdBackupReconciler) handleMissingCredentialSecret(ctx context.Context, backup *hyperv1.HCPEtcdBackup, credentialSecretName string) (ctrl.Result, error) {
	if time.Since(backup.CreationTimestamp.Time) > credentialWaitTimeout {
		return r.setFailedConditionAndUpdate(ctx, backup, hyperv1.BackupFailedReason,
			fmt.Sprintf("timed out after %s waiting for credential Secret %q in namespace %q",
				credentialWaitTimeout, credentialSecretName, r.OperatorNamespace))
	}
	return r.setWaitingConditionAndUpdate(ctx, backup, hyperv1.BackupWaitingForCredentialsReason,
		fmt.Sprintf("waiting for credential Secret %q in namespace %q", credentialSecretName, r.OperatorNamespace))
}

// getCredentialSecret looks up the credential Secret from the cache. The raw
// error is returned so callers can distinguish NotFound (a transient cache miss
// while waiting for the Secret to land) from other failures.
func (r *HCPEtcdBackupReconciler) getCredentialSecret(ctx context.Context, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: name, Namespace: r.OperatorNamespace}
	if err := r.Get(ctx, key, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// backupsForCredentialSecret maps a credential Secret in the operator namespace
// to the non-terminal HCPEtcdBackup(s) that reference it, so those backups are
// re-reconciled as soon as the Secret lands in the cache.
func (r *HCPEtcdBackupReconciler) backupsForCredentialSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() != r.OperatorNamespace {
		return nil
	}

	backupList := &hyperv1.HCPEtcdBackupList{}
	if err := r.List(ctx, backupList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range backupList.Items {
		backup := &backupList.Items[i]
		if isTerminal(backup) {
			continue
		}
		credentialSecretName, err := r.getCredentialSecretName(backup)
		if err != nil || credentialSecretName != obj.GetName() {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      backup.Name,
				Namespace: backup.Namespace,
			},
		})
	}
	return requests
}

func (r *HCPEtcdBackupReconciler) validatePrerequisites(ctx context.Context, backup *hyperv1.HCPEtcdBackup) (ctrl.Result, bool, error) {
	credentialSecretName, err := r.getCredentialSecretName(backup)
	if err != nil {
		result, updateErr := r.setFailedConditionAndUpdate(ctx, backup, hyperv1.BackupFailedReason, err.Error())
		if updateErr != nil {
			return result, true, updateErr
		}
		return result, true, nil
	}

	if _, credErr := r.getCredentialSecret(ctx, credentialSecretName); apierrors.IsNotFound(credErr) {
		// The Secret may not yet be visible in the cache (informer race). Mark the
		// backup as waiting (non-terminal) and requeue; the Secret watch re-triggers
		// reconciliation as soon as it lands. Once the wait deadline is exceeded the
		// backup is failed terminally so shared resources are cleaned up.
		result, updateErr := r.handleMissingCredentialSecret(ctx, backup, credentialSecretName)
		if updateErr != nil {
			return result, true, updateErr
		}
		return result, true, nil
	} else if credErr != nil {
		return ctrl.Result{}, true, fmt.Errorf("failed to get credential Secret: %w", credErr)
	}
	return ctrl.Result{}, false, nil
}

func (r *HCPEtcdBackupReconciler) createResourcesAndJob(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcp *hyperv1.HostedControlPlane) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	credentialSecretName, err := r.getCredentialSecretName(backup)
	if err != nil {
		return r.setFailedConditionAndUpdate(ctx, backup, hyperv1.BackupFailedReason, err.Error())
	}
	credSecret, err := r.getCredentialSecret(ctx, credentialSecretName)
	if apierrors.IsNotFound(err) {
		// Transient cache miss — wait for the Secret rather than failing terminally,
		// up to the wait deadline, after which the backup is failed terminally.
		return r.handleMissingCredentialSecret(ctx, backup, credentialSecretName)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get credential Secret: %w", err)
	}

	creds := resolveCredentials(backup.Spec.Storage.StorageType, credSecret)
	logger.Info("creating backup resources", "backup", backup.Name, "namespace", backup.Namespace, "credentialMode", creds.Mode)

	if err := r.ensureServiceAccount(ctx, creds); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure ServiceAccount: %w", err)
	}

	if err := r.ensureRBAC(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure RBAC: %w", err)
	}

	if err := r.ensureNetworkPolicy(ctx, backup, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure NetworkPolicy: %w", err)
	}

	if err := r.createBackupJob(ctx, backup, hcp, creds); err != nil {
		if apierrors.IsNotFound(err) {
			// Clean up RBAC and NetworkPolicy created above before marking terminal.
			if cleanupErr := r.cleanupResources(ctx, backup); cleanupErr != nil {
				logger.Error(cleanupErr, "failed to cleanup resources after terminal backup failure")
			}
			return r.setFailedConditionAndUpdate(ctx, backup, hyperv1.BackupFailedReason, err.Error())
		}
		return ctrl.Result{}, fmt.Errorf("failed to create backup Job: %w", err)
	}

	r.setCondition(backup, metav1.Condition{
		Type:    string(hyperv1.BackupCompleted),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.BackupInProgressReason,
		Message: "Backup Job created, waiting for completion",
	})
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

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

func (r *HCPEtcdBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !featuregate.Gate().Enabled(featuregate.HCPEtcdBackup) {
		return ctrl.Result{}, nil
	}

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

	hcp, err := r.getHostedControlPlane(ctx, backup.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up HostedControlPlane: %w", err)
	}
	if hcp == nil {
		return r.setFailedConditionAndUpdate(ctx, backup, hyperv1.BackupFailedReason,
			"HostedControlPlane not found in namespace "+backup.Namespace)
	}

	healthy, msg, err := r.checkEtcdHealth(ctx, hcp)
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

	existingJob, err := r.findJobForBackup(ctx, backup)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to find job for backup: %w", err)
	}
	if existingJob != nil {
		return r.handleJobStatus(ctx, backup, existingJob, hcp)
	}

	// Serial execution guard: reject if another backup's Job is already active.
	// This runs after findJobForBackup so we don't reject our own Job.
	activeJob, err := r.findActiveJob(ctx, backup, backup.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for active jobs: %w", err)
	}
	if activeJob != nil {
		logger.Info("rejecting backup: another backup Job is already active", "activeJob", activeJob.Name)
		return r.setFailedConditionAndUpdate(ctx, backup, hyperv1.BackupRejectedReason,
			fmt.Sprintf("rejected: backup Job %q is already running for this HCP; delete this CR and retry after the active backup completes", activeJob.Name))
	}

	// Validate prerequisites before creating any resources.
	// Check credential Secret early so we don't create RBAC/NetworkPolicy unnecessarily.
	if result, done, err := r.validatePrerequisites(ctx, backup); done || err != nil {
		return result, err
	}

	return r.createResourcesAndJob(ctx, backup, hcp)
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
	originalHCP := hcp.DeepCopy()
	condition.ObservedGeneration = hcp.Generation
	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
	return r.Status().Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{}))
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

// etcdShardInfo contains the internal name, public identity, and client
// endpoint for a single etcd shard to be backed up. The internal name is the
// CPO resource naming convention ("etcd" for the default shard,
// "etcd-{spec.name}" for named shards) used for StatefulSets, Services, and
// pod labels. The identity is the public shard identity reported in
// HCPEtcdBackup.status.shardSnapshots: "default" for the default shard and
// the exact ManagedEtcdShardSpec.name for named shards.
type etcdShardInfo struct {
	name     string
	identity string
	endpoint string
}

// defaultShardIdentity is the public identity of the default etcd shard.
const defaultShardIdentity = "default"

// etcdShards returns the list of etcd shards to back up for the given HCP.
// EmptyDir-backed shards are skipped since they hold ephemeral data.
func etcdShards(hcp *hyperv1.HostedControlPlane) []etcdShardInfo {
	shards := []etcdShardInfo{{
		name:     "etcd",
		identity: defaultShardIdentity,
		endpoint: fmt.Sprintf("https://%s.%s.svc:%d",
			etcdutil.ClientServiceName("etcd"), hcp.Namespace, supportconfig.EtcdClientPort),
	}}
	if hcp.Spec.Etcd.Managed != nil {
		for _, s := range hcp.Spec.Etcd.Managed.Shards {
			if s.Storage.Type == hyperv1.EmptyDirEtcdShardStorage {
				continue
			}
			shardName := fmt.Sprintf("etcd-%s", s.Name)
			shards = append(shards, etcdShardInfo{
				name:     shardName,
				identity: s.Name,
				endpoint: fmt.Sprintf("https://%s.%s.svc:%d",
					etcdutil.ClientServiceName(shardName), hcp.Namespace, supportconfig.EtcdClientPort),
			})
		}
	}
	return shards
}

// checkEtcdHealth verifies that all etcd shard StatefulSets have all replicas ready.
func (r *HCPEtcdBackupReconciler) checkEtcdHealth(ctx context.Context, hcp *hyperv1.HostedControlPlane) (bool, string, error) {
	for _, shard := range etcdShards(hcp) {
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: shard.name, Namespace: hcp.Namespace}, sts); err != nil {
			if apierrors.IsNotFound(err) {
				return false, fmt.Sprintf("etcd StatefulSet %q not found", shard.name), nil
			}
			return false, "", err
		}
		desired := ptr.Deref(sts.Spec.Replicas, 1)
		// A shard scaled to zero is not backable: the snapshot init container
		// would target pods that do not exist and the Job would fail. Gate it
		// here instead, on the same non-terminal EtcdUnhealthy path as other
		// not-ready states, so the backup retries once the shard is scaled
		// back up. (Note ptr.Deref above: a nil Replicas pointer defaults to 1,
		// so desired == 0 can only mean an explicit scale-down.)
		if desired == 0 {
			return false, fmt.Sprintf("etcd StatefulSet %q is scaled to zero", shard.name), nil
		}
		if sts.Status.ReadyReplicas < desired {
			return false, fmt.Sprintf("etcd StatefulSet %q not fully ready: %d/%d replicas ready",
				shard.name, sts.Status.ReadyReplicas, desired), nil
		}
	}
	return true, "", nil
}

// findActiveJob checks if any backup Job is currently active for the given HCP
// namespace. It uses a quorum read (bypassing the informer cache) so a Job
// created moments ago by another reconcile is already visible, and it skips
// Jobs belonging to the given backup so a backup is never rejected by its own
// Job (its own Job is handled earlier via findJobForBackup).
//
// A Job counts as active when it has not yet reached a terminal state
// (Complete or Failed). A freshly created Job carries no terminal condition
// until the job controller asynchronously sets status.active, so keying on
// status.active alone would leave a window in which two concurrent backups
// could both pass this guard.
func (r *HCPEtcdBackupReconciler) findActiveJob(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcpNamespace string) (*batchv1.Job, error) {
	jobList := &batchv1.JobList{}
	if err := r.apiReader().List(ctx, jobList,
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
		if job.Labels[LabelBackupName] == backup.Name {
			continue
		}
		if !jobHasTerminated(job) {
			return job, nil
		}
	}
	return nil, nil
}

// jobHasTerminated reports whether a backup Job has reached a terminal state
// (Complete or Failed). Jobs without a terminal condition are pending or
// running, and count as active for serialization purposes.
func jobHasTerminated(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if cond.Type == batchv1.JobComplete || cond.Type == batchv1.JobFailed {
			return true
		}
	}
	return false
}

// hasNonTerminalBackup checks if any other HCPEtcdBackup in the same namespace
// is not yet in a terminal state (pending or in-progress). This guards against
// deleting shared RBAC/NetworkPolicy resources while another backup still needs them,
// covering the race window between HCPEtcdBackup creation and Job creation.
func (r *HCPEtcdBackupReconciler) hasNonTerminalBackup(ctx context.Context, current *hyperv1.HCPEtcdBackup) (bool, string, error) {
	backupList := &hyperv1.HCPEtcdBackupList{}
	if err := r.List(ctx, backupList, client.InNamespace(current.Namespace)); err != nil {
		return false, "", err
	}

	for i := range backupList.Items {
		other := &backupList.Items[i]
		if other.Name == current.Name {
			continue
		}
		if !isTerminal(other) {
			return true, other.Name, nil
		}
	}
	return false, "", nil
}

// findJobForBackup finds the Job created for this specific backup. It uses a
// quorum read so a Job created by an earlier reconcile is found even before
// the informer cache has observed it, preventing duplicate Job creation
// during the cache-lag window.
func (r *HCPEtcdBackupReconciler) findJobForBackup(ctx context.Context, backup *hyperv1.HCPEtcdBackup) (*batchv1.Job, error) {
	jobList := &batchv1.JobList{}
	if err := r.apiReader().List(ctx, jobList,
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

			// Extract the shard snapshot results: from the result ConfigMap
			// (multi-shard), falling back to the upload container's
			// termination message (single-shard legacy).
			shardSnapshots, err := r.getShardSnapshots(ctx, backup, job)
			if err != nil {
				// Retry transient extraction failures (API errors, pod status
				// lag) before failing the backup terminally. Success is never
				// recorded without the per-shard results.
				return r.retryShardSnapshotExtraction(ctx, backup, hcp, err)
			}

			// Require exactly one valid result per expected shard before
			// recording terminal success: a partial or unexpected result set
			// must not report BackupSucceeded.
			if problems := validateShardSnapshotResults(shardSnapshots, expectedShardNames(job, hcp)); len(problems) > 0 {
				return r.failIncompleteResults(ctx, backup, hcp, problems)
			}

			// Cleanup temporary RBAC and NetworkPolicy as soon as the Job completes.
			// This must happen before any status updates that could fail and cause
			// requeue, to avoid leaving security resources exposed indefinitely.
			if err := r.cleanupResources(ctx, backup); err != nil {
				logger.Error(err, "failed to cleanup resources after successful backup")
			}

			// Populate status with shard snapshots and backward-compat SnapshotURL.
			// The completeness check above guarantees exactly one result per
			// expected shard, so the list is never empty here.
			backup.Status.ShardSnapshots = shardSnapshots
			// Set the default shard's URL for backward compatibility. Messages
			// from images built before the public shard identity was introduced
			// report the default shard as "etcd"; accept both.
			for _, ss := range shardSnapshots {
				if ss.Name == defaultShardIdentity || ss.Name == "etcd" {
					backup.Status.SnapshotURL = ss.SnapshotURL
					break
				}
			}

			// Persist the snapshot URL in the HostedCluster status BEFORE marking
			// the backup as terminal so the controller retries on requeue.
			// This is idempotent: if it succeeds but the backup status update below
			// fails, the next reconcile re-extracts the URL and writes the same value.
			if backup.Status.SnapshotURL != "" {
				if err := r.updateHostedClusterBackupURL(ctx, hcp, backup.Status.SnapshotURL); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update HostedCluster LastSuccessfulEtcdBackupURL: %w", err)
				}
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

			// The results are now persisted in the backup status; the result
			// ConfigMap is no longer needed.
			r.deleteResultConfigMap(ctx, backup)

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

			// A failed Job's result ConfigMap (if any) is not needed.
			r.deleteResultConfigMap(ctx, backup)

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

// retryShardSnapshotExtraction handles a failed attempt to extract the shard
// snapshot results from a completed backup Job. Transient failures (API
// errors, pod status lag) are retried up to maxResultExtractionAttempts times
// before the backup is failed terminally, so a completed backup never records
// success without its per-shard results.
func (r *HCPEtcdBackupReconciler) retryShardSnapshotExtraction(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcp *hyperv1.HostedControlPlane, extractionErr error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	attempts := 1
	if v, ok := backup.Annotations[annotationResultExtractionAttempts]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			attempts = n + 1
		}
	}

	if attempts >= maxResultExtractionAttempts {
		message := fmt.Sprintf("failed to extract shard snapshot results from the completed backup Job after %d attempts: %v", attempts, extractionErr)
		r.deleteResultConfigMap(ctx, backup)
		if err := r.cleanupResources(ctx, backup); err != nil {
			logger.Error(err, "failed to cleanup resources after failed result extraction")
		}
		r.setCondition(backup, metav1.Condition{
			Type:    string(hyperv1.BackupCompleted),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.BackupResultExtractionFailedReason,
			Message: message,
		})
		if err := r.Status().Update(ctx, backup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
		}
		if err := r.updateHCPBackupCondition(ctx, hcp, metav1.Condition{
			Type:    string(hyperv1.EtcdBackupSucceeded),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.BackupResultExtractionFailedReason,
			Message: fmt.Sprintf("Backup %q failed: %s", backup.Name, message),
		}); err != nil {
			logger.Error(err, "failed to update HCP backup condition")
		}
		return ctrl.Result{}, nil
	}

	// Record the attempt on the backup and requeue; the next reconcile
	// re-runs handleJobStatus against the completed Job.
	base := backup.DeepCopy()
	if backup.Annotations == nil {
		backup.Annotations = map[string]string{}
	}
	backup.Annotations[annotationResultExtractionAttempts] = strconv.Itoa(attempts)
	if err := r.Patch(ctx, backup, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to record result extraction attempt: %w", err)
	}
	logger.Error(extractionErr, "failed to read shard snapshots from pod termination message; retrying", "attempt", attempts)
	return ctrl.Result{RequeueAfter: resultExtractionRetryInterval}, nil
}

// failIncompleteResults marks the backup terminally failed because the
// completed Job's shard snapshot results do not contain exactly one valid
// entry per expected shard.
func (r *HCPEtcdBackupReconciler) failIncompleteResults(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcp *hyperv1.HostedControlPlane, problems []string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	message := fmt.Sprintf("backup Job completed but shard snapshot results are incomplete: %s", strings.Join(problems, "; "))
	r.deleteResultConfigMap(ctx, backup)
	if err := r.cleanupResources(ctx, backup); err != nil {
		logger.Error(err, "failed to cleanup resources after incomplete backup results")
	}
	r.setCondition(backup, metav1.Condition{
		Type:    string(hyperv1.BackupCompleted),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.BackupResultIncompleteReason,
		Message: message,
	})
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	if err := r.updateHCPBackupCondition(ctx, hcp, metav1.Condition{
		Type:    string(hyperv1.EtcdBackupSucceeded),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.BackupResultIncompleteReason,
		Message: fmt.Sprintf("Backup %q failed: %s", backup.Name, message),
	}); err != nil {
		logger.Error(err, "failed to update HCP backup condition")
	}
	return ctrl.Result{}, nil
}

// getShardSnapshots reads the shard snapshot results for a completed backup
// Job. Multi-shard Jobs write their results to a ConfigMap in the operator
// namespace, which has no size limits; the lookup uses a quorum read so a
// ConfigMap written moments ago is already visible. The pod termination
// message is the legacy channel: single-shard Jobs and images built before
// the result ConfigMap was introduced.
func (r *HCPEtcdBackupReconciler) getShardSnapshots(ctx context.Context, backup *hyperv1.HCPEtcdBackup, job *batchv1.Job) ([]hyperv1.HCPEtcdShardSnapshot, error) {
	cm := &corev1.ConfigMap{}
	err := r.apiReader().Get(ctx, types.NamespacedName{
		Name:      resultConfigMapName(backup),
		Namespace: r.OperatorNamespace,
	}, cm)
	if err == nil {
		return parseShardSnapshots(cm.Data[resultConfigMapKey])
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to read result ConfigMap: %w", err)
	}
	return r.getShardSnapshotsFromPod(ctx, job)
}

// getShardSnapshotsFromPod reads the per-shard snapshot URLs from the upload
// container's termination message in the Pod controlled by the given Job.
// The termination message is a JSON array of {"name":"...","url":"..."}.
// For backward compatibility with older CPO images that write a plain URL,
// a single plain URL is treated as the default shard snapshot.
func (r *HCPEtcdBackupReconciler) getShardSnapshotsFromPod(ctx context.Context, job *batchv1.Job) ([]hyperv1.HCPEtcdShardSnapshot, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(job.Namespace),
		client.MatchingLabels{"batch.kubernetes.io/job-name": job.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list pods for job %q: %w", job.Name, err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "upload" && cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				msg := strings.TrimSpace(cs.State.Terminated.Message)
				return parseShardSnapshots(msg)
			}
		}
	}
	// The Job is complete, so the upload container must have terminated and
	// written its results. An absent pod or empty message is an extraction
	// failure the caller retries (and eventually fails the backup on), not a
	// silent success with missing results.
	return nil, fmt.Errorf("no terminated upload container termination message found for job %q", job.Name)
}

// shardSnapshotJSON is the JSON format written by the etcd-upload command.
type shardSnapshotJSON struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// parseShardSnapshots parses the termination message from the upload container.
// Accepts either a JSON array of shard snapshots or a plain URL (backward compat).
func parseShardSnapshots(msg string) ([]hyperv1.HCPEtcdShardSnapshot, error) {
	if strings.HasPrefix(msg, "[") {
		var entries []shardSnapshotJSON
		if err := json.Unmarshal([]byte(msg), &entries); err != nil {
			return nil, fmt.Errorf("failed to parse shard snapshots JSON: %w", err)
		}
		var snapshots []hyperv1.HCPEtcdShardSnapshot
		for _, e := range entries {
			if e.Name == "" || !isSupportedSnapshotURL(e.URL) {
				continue
			}
			snapshots = append(snapshots, hyperv1.HCPEtcdShardSnapshot{
				Name:        e.Name,
				SnapshotURL: e.URL,
			})
		}
		return snapshots, nil
	}
	// Backward compat: plain URL from older CPO images.
	if isSupportedSnapshotURL(msg) {
		return []hyperv1.HCPEtcdShardSnapshot{{
			Name:        defaultShardIdentity,
			SnapshotURL: msg,
		}}, nil
	}
	return nil, nil
}

func isSupportedSnapshotURL(rawURL string) bool {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Host == "" {
		return false
	}
	return parsedURL.Scheme == "https" || parsedURL.Scheme == "s3"
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
// For Azure Workload Identity mode, if the SA already has a
// azure.workload.identity/client-id annotation (e.g., set by infrastructure/Helm),
// it is preserved. Otherwise, the annotation is set from the credential secret.
func (r *HCPEtcdBackupReconciler) ensureServiceAccount(ctx context.Context, creds resolvedCredentials) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobServiceAccountName,
			Namespace: r.OperatorNamespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if creds.needsWorkloadIdentityLabel() {
			if sa.Annotations == nil {
				sa.Annotations = map[string]string{}
			}
			if sa.Annotations["azure.workload.identity/client-id"] == "" {
				sa.Annotations["azure.workload.identity/client-id"] = creds.ClientID
			}
		} else {
			delete(sa.Annotations, "azure.workload.identity/client-id")
		}
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

	// Role in the operator namespace granting the backup Job's service
	// account create/update access to exactly its result ConfigMap, where
	// etcd-upload stores the multi-shard snapshot results.
	resultRBACName := resultConfigMapName(backup)
	resultRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resultRBACName,
			Namespace: r.OperatorNamespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, resultRole, func() error {
		resultRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{resultRBACName},
				Verbs:         []string{"create", "update", "patch", "get"},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure result Role: %w", err)
	}

	resultRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resultRBACName,
			Namespace: r.OperatorNamespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, resultRB, func() error {
		resultRB.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     resultRBACName,
		}
		resultRB.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      jobServiceAccountName,
				Namespace: r.OperatorNamespace,
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure result RoleBinding: %w", err)
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
// When sharding is enabled, the pod selector is widened to cover all shard
// StatefulSet pods (which have labels like app: etcd-events).
func (r *HCPEtcdBackupReconciler) ensureNetworkPolicy(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcp *hyperv1.HostedControlPlane) error {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkPolicyName,
			Namespace: backup.Namespace,
		},
	}

	// Build list of app label values for all shards being backed up.
	shards := etcdShards(hcp)
	appLabels := make([]string, 0, len(shards))
	for _, s := range shards {
		appLabels = append(appLabels, s.name)
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		etcdPort := intstr.FromInt32(supportconfig.EtcdClientPort)
		tcpProtocol := corev1.ProtocolTCP
		np.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "app",
					Operator: metav1.LabelSelectorOpIn,
					Values:   appLabels,
				}},
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
// It skips deletion if another backup is pending or in-progress in the same namespace,
// because the shared resources (NetworkPolicy, RBAC) may be needed by that backup.
func (r *HCPEtcdBackupReconciler) cleanupResources(ctx context.Context, backup *hyperv1.HCPEtcdBackup) error {
	logger := log.FromContext(ctx)

	// Guard: don't delete shared resources while another non-terminal backup exists.
	// This covers both the case where a Job is active AND the race window where a
	// new HCPEtcdBackup has been created but its Job hasn't been spawned yet.
	hasOther, otherName, err := r.hasNonTerminalBackup(ctx, backup)
	if err != nil {
		return fmt.Errorf("failed to check for non-terminal backups before cleanup: %w", err)
	}
	if hasOther {
		logger.Info("skipping cleanup: another backup is still pending or in-progress", "otherBackup", otherName)
		return nil
	}

	var firstErr error

	// The result Role/RoleBinding are named after this backup and never
	// shared, so they can be deleted unconditionally (unlike the shared
	// resources below, which the guard protects).
	for _, obj := range []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: resultConfigMapName(backup), Namespace: r.OperatorNamespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: resultConfigMapName(backup), Namespace: r.OperatorNamespace}},
	} {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to delete backup result RBAC", "name", resultConfigMapName(backup))
			firstErr = err
		}
	}

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
func (r *HCPEtcdBackupReconciler) createBackupJob(ctx context.Context, backup *hyperv1.HCPEtcdBackup, hcp *hyperv1.HostedControlPlane, creds resolvedCredentials) error {
	// Resolve images
	pullSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: pullSecretName, Namespace: backup.Namespace}, pullSecret); err != nil {
		// Preserve error type (including IsNotFound) so caller can detect permanent failures
		return fmt.Errorf("pull secret %q in namespace %q: %w", pullSecretName, backup.Namespace, err)
	}
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]

	releaseImage := imageregistry.HCPControlPlaneReleaseImage(hcp)

	cpoImage, err := r.resolveControlPlaneOperatorImage(ctx, hcp, releaseImage, pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to resolve CPO image: %w", err)
	}

	etcdImage, err := imageregistry.GetPayloadImageFromRelease(ctx, r.ReleaseProvider, releaseImage, "etcd", pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to resolve etcd image: %w", err)
	}

	// Resolve etcd shards to back up
	shards := etcdShards(hcp)

	// Build upload args based on storage type and credential mode
	uploadArgs, err := r.buildUploadArgs(backup, shards, creds)
	if err != nil {
		return fmt.Errorf("failed to build upload args: %w", err)
	}

	// Generate the attempt timestamp here so the exact object key prefix is
	// known before the Job runs, and pass it to etcd-upload: the controller
	// records it in the backup status (attemptKeyPrefix) so operators can
	// identify, and clean up, objects left behind by failed attempts without
	// needing signed URLs.
	attemptTimestamp := time.Now().Unix()
	uploadArgs = append(uploadArgs, "--attempt-timestamp", strconv.FormatInt(attemptTimestamp, 10))
	backup.Status.AttemptKeyPrefix = attemptKeyPrefix(backup, attemptTimestamp)

	jobLabels := map[string]string{
		LabelApp:          LabelName,
		labelHCP:          hcp.Name,
		LabelBackupName:   backup.Name,
		LabelHCPNamespace: backup.Namespace,
	}

	podLabels := make(map[string]string, len(jobLabels)+1)
	for k, v := range jobLabels {
		podLabels[k] = v
	}
	if creds.needsWorkloadIdentityLabel() {
		podLabels["azure.workload.identity/use"] = "true"
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("etcd-backup-%s-", backup.Name),
			Namespace:    r.OperatorNamespace,
			Labels:       jobLabels,
			Annotations: map[string]string{
				// Record the shard identities this Job was built to snapshot so
				// the completion path can require exactly one result per shard,
				// even if the HCP spec changes while the Job runs.
				annotationExpectedShardSnapshots: strings.Join(shardIdentities(shards), ","),
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ptr.To[int32](600),
			ActiveDeadlineSeconds:   ptr.To[int64](backupJobDeadlineSeconds(shards, backup)),
			BackoffLimit:            ptr.To[int32](0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: jobServiceAccountName,
					RestartPolicy:      corev1.RestartPolicyNever,
					Volumes:            r.buildJobVolumes(creds),
					InitContainers:     r.buildSnapshotInitContainers(cpoImage, etcdImage, backup.Namespace, shards),
					Containers: []corev1.Container{
						r.buildUploadContainer(cpoImage, uploadArgs, creds),
					},
				},
			},
		},
	}

	return r.Create(ctx, job)
}

// shardIdentities returns the public identities of the given shards: "default"
// for the default shard and the exact ManagedEtcdShardSpec.name for named
// shards. This is the identity reported in shardSnapshots.
func shardIdentities(shards []etcdShardInfo) []string {
	identities := make([]string, 0, len(shards))
	for _, s := range shards {
		identities = append(identities, s.identity)
	}
	return identities
}

// expectedShardNames returns the shard identities a completed backup Job is
// expected to have snapshotted. It prefers the annotation recorded when the
// Job was created; Jobs created before the annotation existed fall back to
// the shards currently configured on the HCP.
func expectedShardNames(job *batchv1.Job, hcp *hyperv1.HostedControlPlane) []string {
	if v, ok := job.Annotations[annotationExpectedShardSnapshots]; ok && v != "" {
		return strings.Split(v, ",")
	}
	return shardIdentities(etcdShards(hcp))
}

// validateShardSnapshotResults requires exactly one result per expected
// shard and reports any missing, duplicate, or unexpected entries. Entries
// the parser dropped as invalid (unsupported scheme, unnamed) surface here
// as missing shards rather than being silently ignored.
func validateShardSnapshotResults(snapshots []hyperv1.HCPEtcdShardSnapshot, expected []string) []string {
	counts := make(map[string]int, len(snapshots))
	for _, s := range snapshots {
		counts[s.Name]++
	}
	expectedSet := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		expectedSet[name] = struct{}{}
	}
	var problems []string
	for _, name := range expected {
		switch counts[name] {
		case 0:
			problems = append(problems, fmt.Sprintf("missing result for shard %q", name))
		case 1:
		default:
			problems = append(problems, fmt.Sprintf("duplicate results for shard %q", name))
		}
	}
	for name, n := range counts {
		if _, ok := expectedSet[name]; !ok && n > 0 {
			problems = append(problems, fmt.Sprintf("unexpected result for shard %q", name))
		}
	}
	return problems
}

// backupJobDeadlineSeconds returns the backup Job's active deadline. The
// deadline scales with the shard count and storage provider: see the
// backupJob*DeadlineSeconds constants.
func backupJobDeadlineSeconds(shards []etcdShardInfo, backup *hyperv1.HCPEtcdBackup) int64 {
	base, perShard := backupJobBaseDeadlineSeconds, backupJobPerShardDeadlineSeconds
	if backup.Spec.Storage.StorageType == hyperv1.AzureBlobBackupStorage {
		base, perShard = azureBackupJobBaseDeadlineSeconds, azureBackupJobPerShardDeadlineSeconds
	}
	return base + perShard*int64(len(shards)-1)
}

// resultConfigMapName returns the deterministic name of the ConfigMap, in
// the HyperShift operator namespace, where a backup Job stores its
// multi-shard snapshot results. The name is derived from the backup (not the
// Job, whose name is generated) so both the Job builder and the completion
// path can compute it, and so the Job's RBAC can be scoped to exactly this
// ConfigMap.
func resultConfigMapName(backup *hyperv1.HCPEtcdBackup) string {
	return fmt.Sprintf("etcd-backup-result-%s-%s", backup.Namespace, backup.Name)
}

// deleteResultConfigMap deletes a backup's result ConfigMap. It is called
// once the results have been persisted to the backup status (or the backup
// has failed terminally), so the ConfigMap is not needed anymore. The
// ConfigMap intentionally survives transient extraction retries.
func (r *HCPEtcdBackupReconciler) deleteResultConfigMap(ctx context.Context, backup *hyperv1.HCPEtcdBackup) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resultConfigMapName(backup),
			Namespace: r.OperatorNamespace,
		},
	}
	if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		log.FromContext(ctx).Error(err, "failed to delete backup result ConfigMap")
	}
}

// attemptKeyPrefix returns the object key prefix for a backup attempt: the
// storage keyPrefix from the spec joined with the attempt timestamp. Objects
// written by the attempt are <prefix>.db (single shard) or <prefix>/<shard>.db
// (multi shard).
func attemptKeyPrefix(backup *hyperv1.HCPEtcdBackup, timestamp int64) string {
	var keyPrefix string
	switch backup.Spec.Storage.StorageType {
	case hyperv1.S3BackupStorage:
		keyPrefix = backup.Spec.Storage.S3.KeyPrefix
	case hyperv1.AzureBlobBackupStorage:
		keyPrefix = backup.Spec.Storage.AzureBlob.KeyPrefix
	}
	keyPrefix = strings.TrimSuffix(keyPrefix, "/")
	return fmt.Sprintf("%s/%d", keyPrefix, timestamp)
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

// buildSnapshotInitContainers creates the init container list for the backup Job:
// one fetch-certs container followed by one snapshot container per shard.
// All shards share the same etcd-client-tls credentials because the single etcd CA
// signs all shard certificates and KAS uses one --etcd-certfile/keyfile pair.
func (r *HCPEtcdBackupReconciler) buildSnapshotInitContainers(cpoImage, etcdImage, namespace string, shards []etcdShardInfo) []corev1.Container {
	initContainers := []corev1.Container{
		{
			Name:  "fetch-certs",
			Image: cpoImage,
			Command: []string{
				"control-plane-operator", "fetch-etcd-certs",
				"--hcp-namespace", namespace,
				"--output-dir", mountPathEtcdCerts,
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      volumeEtcdCerts,
					MountPath: mountPathEtcdCerts,
				},
			},
		},
	}

	for _, shard := range shards {
		initContainers = append(initContainers, corev1.Container{
			Name:  fmt.Sprintf("snapshot-%s", shard.identity),
			Image: etcdImage,
			Env: []corev1.EnvVar{
				{Name: "ETCDCTL_API", Value: "3"},
			},
			Command: []string{
				"/usr/bin/etcdctl",
				"--endpoints", shard.endpoint,
				"--cacert", mountPathEtcdCerts + "/ca.crt",
				"--cert", mountPathEtcdCerts + "/etcd-client.crt",
				"--key", mountPathEtcdCerts + "/etcd-client.key",
				"snapshot", "save",
				fmt.Sprintf("%s/%s.db", mountPathEtcdBackup, shard.identity),
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
		})
	}

	return initContainers
}

func (r *HCPEtcdBackupReconciler) buildJobVolumes(creds resolvedCredentials) []corev1.Volume {
	volumes := []corev1.Volume{
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
	}

	if creds.needsCredentialsFile() {
		credentialsKey := creds.CredentialsKey
		if credentialsKey == "" {
			credentialsKey = secretKeyCredentials
		}
		volumes = append(volumes, corev1.Volume{
			Name: volumeCredentials,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: creds.SecretName,
					Items: []corev1.KeyToPath{
						{Key: credentialsKey, Path: secretKeyCredentials},
					},
				},
			},
		})
	}

	if creds.needsProjectedToken() {
		volumes = append(volumes, corev1.Volume{
			Name: volumeAWSIAMToken,
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Audience:          "sts.amazonaws.com",
								ExpirationSeconds: ptr.To[int64](3600),
								Path:              "token",
							},
						},
					},
				},
			},
		})
	}

	return volumes
}

func (r *HCPEtcdBackupReconciler) buildUploadContainer(image string, args []string, creds resolvedCredentials) corev1.Container {
	container := corev1.Container{
		Name:    "upload",
		Image:   image,
		Command: args,
		// The termination message is only used by the legacy single-shard
		// path (a plain URL, well under the 4096-byte limit).
		// FallbackToLogsOnError surfaces container logs as the message when
		// the upload container exits non-zero, which aids diagnosis of failed
		// uploads. Multi-shard results flow through the result ConfigMap
		// instead, which has no size limit.
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volumeEtcdBackup,
				MountPath: mountPathEtcdBackup,
				ReadOnly:  true,
			},
		},
	}

	if creds.needsCredentialsFile() {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeCredentials,
			MountPath: mountPathCredentials,
			ReadOnly:  true,
		})
	}

	if creds.needsProjectedToken() {
		container.Env = []corev1.EnvVar{
			{Name: "AWS_ROLE_ARN", Value: creds.RoleARN},
			{Name: "AWS_WEB_IDENTITY_TOKEN_FILE", Value: mountPathAWSIAMToken + "/token"},
		}
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeAWSIAMToken,
			MountPath: mountPathAWSIAMToken,
			ReadOnly:  true,
		})
	}

	return container
}

// buildUploadArgs constructs the command args for the etcd-upload container.
// For single-shard clusters, --snapshot-path is used for backward compatibility
// with older CPO images that don't support --snapshot-dir. Multi-shard clusters
// use --snapshot-dir which uploads all .db files in the directory.
func (r *HCPEtcdBackupReconciler) buildUploadArgs(backup *hyperv1.HCPEtcdBackup, shards []etcdShardInfo, creds resolvedCredentials) ([]string, error) {
	args := []string{"control-plane-operator", "etcd-upload"}
	if len(shards) == 1 {
		args = append(args, "--snapshot-path", fmt.Sprintf("%s/%s.db", mountPathEtcdBackup, shards[0].identity))
	} else {
		args = append(args, "--snapshot-dir", mountPathEtcdBackup)
		// Multi-shard results are stored in a ConfigMap (no size limits)
		// rather than the pod termination message.
		args = append(args, "--result-configmap", resultConfigMapName(backup))
	}

	switch backup.Spec.Storage.StorageType {
	case hyperv1.S3BackupStorage:
		s3 := backup.Spec.Storage.S3
		args = append(args,
			"--storage-type", "S3",
			"--aws-bucket", s3.Bucket,
			"--aws-region", s3.Region,
			"--key-prefix", s3.KeyPrefix,
		)
		if creds.needsCredentialsFile() {
			args = append(args, "--credentials-file", mountPathCredentials+"/credentials")
		}
		if s3.KMSKeyARN != "" {
			args = append(args, "--aws-kms-key-arn", s3.KMSKeyARN)
		}
		return args, nil

	case hyperv1.AzureBlobBackupStorage:
		azure := backup.Spec.Storage.AzureBlob
		args = append(args,
			"--storage-type", "AzureBlob",
			"--azure-container", azure.Container,
			"--azure-storage-account", azure.StorageAccount,
			"--key-prefix", azure.KeyPrefix,
		)
		if creds.needsCredentialsFile() {
			args = append(args, "--credentials-file", mountPathCredentials+"/credentials")
		}
		if authType := creds.azureAuthType(); authType != "" {
			args = append(args, "--azure-auth-type", authType)
		}
		if azure.EncryptionKeyURL != "" {
			args = append(args, "--azure-encryption-scope", azure.EncryptionKeyURL)
		}
		return args, nil
	}

	return nil, fmt.Errorf("unsupported storage type: %s", backup.Spec.Storage.StorageType)
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
