// Package reencryption implements the HCCO re-encryption controller that
// manages etcd data re-encryption after encryption key rotation.
package reencryption

import (
	"context"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	kasaescbc "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/secretencryption"
	"github.com/openshift/hypershift/support/util"

	"github.com/openshift/library-go/pkg/operator/encryption/controllers/migrators"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
)

const (
	// maxHistoryEntries is the maximum number of entries kept in the rotation history.
	maxHistoryEntries = 5

	requeueWaitingForKAS     = 30 * time.Second
	requeueMigrationProgress = 10 * time.Second
	requeueMigrationFailure  = 60 * time.Second
)

// Reconciler watches for encryption key changes on the HCP spec and drives the
// multi-phase key rotation lifecycle: ReadOnlyDeploy -> WritePromote -> Migrating -> Completed.
type Reconciler struct {
	cpClient     crclient.Client
	guestClient  crclient.Client
	hcpName      string
	hcpNamespace string
	migrator     migrators.Migrator
	now          func() time.Time
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	hcp := &hyperv1.HostedControlPlane{}
	if err := r.cpClient.Get(ctx, types.NamespacedName{Namespace: r.hcpNamespace, Name: r.hcpName}, hcp); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get HCP: %w", err)
	}

	originalHCP := hcp.DeepCopy()
	result, err := r.reconcile(ctx, log, hcp)
	if err != nil {
		return result, err
	}

	if !equality.Semantic.DeepEqual(hcp.Status, originalHCP.Status) {
		log.Info("Patching HCP status with secret encryption changes")
		patch := crclient.MergeFrom(originalHCP)
		if err := r.cpClient.Status().Patch(ctx, hcp, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to patch HCP status: %w", err)
		}
		log.Info("Successfully patched HCP status")
		recordMigrationState(r.hcpNamespace, r.hcpName, hcp.Status.SecretEncryption)
		previousState := encryptionHistoryState(originalHCP.Status.SecretEncryption)
		currentState := encryptionHistoryState(hcp.Status.SecretEncryption)
		if currentState == hyperv1.EncryptionMigrationStateCompleted && previousState != currentState {
			recordMigrationDuration(r.hcpNamespace, r.hcpName, hcp.Status.SecretEncryption)
		}
	}

	return result, nil
}

func (r *Reconciler) reconcile(ctx context.Context, log logr.Logger, hcp *hyperv1.HostedControlPlane) (reconcile.Result, error) {
	// If encryption is not configured, ensure status is clean.
	if hcp.Spec.SecretEncryption == nil {
		return r.handleEncryptionNotConfigured(hcp)
	}

	// Compute the spec fingerprint from the current spec.
	specKeyStatus, err := r.keyStatusFromSpec(ctx, hcp)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to compute key status from spec: %w", err)
	}
	if specKeyStatus == nil {
		log.Info("Secret encryption configured but key status could not be derived, skipping")
		return reconcile.Result{}, nil
	}

	specFingerprint := secretencryption.FingerprintFromKeyStatus(specKeyStatus)

	statusActiveFingerprint := secretencryption.FingerprintFromKeyStatus(&hcp.Status.SecretEncryption.ActiveKey)

	// Case 1: Status has no active key (first-time setup or upgrade bootstrap).
	if hcp.Status.SecretEncryption.ActiveKey.Provider == "" {
		return r.handleInitialBootstrap(log, hcp, specKeyStatus)
	}

	// Case 2: A rotation is already in progress (targetKey is set).
	if hcp.Status.SecretEncryption.TargetKey.Provider != "" {
		return r.handleInProgressRotation(ctx, log, hcp, specFingerprint)
	}

	// Case 3: No rotation in progress and spec matches status -> steady state.
	if specFingerprint == statusActiveFingerprint {
		log.V(2).Info("Encryption key is up to date, no rotation needed")
		return reconcile.Result{}, nil
	}

	// Case 4: Spec key differs from status active key -> start new rotation.
	return r.startNewRotation(log, hcp, specKeyStatus, specFingerprint, statusActiveFingerprint)
}

func encryptionHistoryState(status hyperv1.SecretEncryptionStatus) hyperv1.EncryptionMigrationState {
	if len(status.History) == 0 {
		return ""
	}
	return status.History[0].State
}

// handleEncryptionNotConfigured clears the targetKey and removes the EtcdDataEncryptionUpToDate condition.
func (r *Reconciler) handleEncryptionNotConfigured(hcp *hyperv1.HostedControlPlane) (reconcile.Result, error) {
	hcp.Status.SecretEncryption.TargetKey = hyperv1.SecretEncryptionKeyStatus{}
	meta.RemoveStatusCondition(&hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
	return reconcile.Result{}, nil
}

// handleInitialBootstrap sets the initial active key from the spec without starting a rotation.
func (r *Reconciler) handleInitialBootstrap(log logr.Logger, hcp *hyperv1.HostedControlPlane, specKeyStatus *hyperv1.SecretEncryptionKeyStatus) (reconcile.Result, error) {
	log.Info("Initializing secret encryption status with active key from spec")
	hcp.Status.SecretEncryption.ActiveKey = *specKeyStatus.DeepCopy()

	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.ReEncryptionCompletedReason,
		Message:            "Encryption key initialized",
		ObservedGeneration: hcp.Generation,
	})
	return reconcile.Result{}, nil
}

// startNewRotation sets the targetKey and creates a history entry for a new rotation.
func (r *Reconciler) startNewRotation(log logr.Logger, hcp *hyperv1.HostedControlPlane, specKeyStatus *hyperv1.SecretEncryptionKeyStatus, specFingerprint, statusActiveFingerprint string) (reconcile.Result, error) {
	log.Info("Encryption key changed, starting new rotation",
		"specFingerprint", specFingerprint,
		"statusActiveFingerprint", statusActiveFingerprint)

	hcp.Status.SecretEncryption.TargetKey = *specKeyStatus.DeepCopy()

	fromRef := secretencryption.KeyReferenceFromStatus(&hcp.Status.SecretEncryption.ActiveKey)
	toRef := secretencryption.KeyReferenceFromStatus(specKeyStatus)

	entry := hyperv1.EncryptionMigrationHistory{
		From:        fromRef,
		To:          toRef,
		State:       hyperv1.EncryptionMigrationStateReadOnlyDeploy,
		StartedTime: metav1.Time{Time: r.now()},
	}

	// Prepend new entry and trim history.
	hcp.Status.SecretEncryption.History = prependHistory(hcp.Status.SecretEncryption.History, entry)

	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
		Status:             metav1.ConditionFalse,
		Reason:             hyperv1.ReadOnlyRolloutInProgressReason,
		Message:            "Encryption key rotation started: deploying new key as read-only",
		ObservedGeneration: hcp.Generation,
	})

	return reconcile.Result{}, nil
}

// handleInProgressRotation derives the current phase from observable state and acts accordingly.
// It inspects the EncryptionConfiguration and KAS Deployment convergence rather than
// reading history[0].state. The history state is updated for observability only.
func (r *Reconciler) handleInProgressRotation(ctx context.Context, log logr.Logger, hcp *hyperv1.HostedControlPlane, specFingerprint string) (reconcile.Result, error) {
	targetFingerprint := secretencryption.FingerprintFromKeyStatus(&hcp.Status.SecretEncryption.TargetKey)

	if specFingerprint != targetFingerprint {
		log.Info("Spec key differs from target key during rotation, current rotation will complete first",
			"specFingerprint", specFingerprint,
			"targetFingerprint", targetFingerprint)
	}

	if len(hcp.Status.SecretEncryption.History) == 0 {
		log.Info("Target key set but no history entry found, creating one")
		fromRef := secretencryption.KeyReferenceFromStatus(&hcp.Status.SecretEncryption.ActiveKey)
		toRef := secretencryption.KeyReferenceFromStatus(&hcp.Status.SecretEncryption.TargetKey)
		entry := hyperv1.EncryptionMigrationHistory{
			From:        fromRef,
			To:          toRef,
			State:       hyperv1.EncryptionMigrationStateReadOnlyDeploy,
			StartedTime: metav1.Time{Time: r.now()},
		}
		hcp.Status.SecretEncryption.History = prependHistory(hcp.Status.SecretEncryption.History, entry)
	}

	// Derive the current phase from observable state.
	role, err := r.deriveTargetKeyRole(ctx, hcp)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to derive target key role from EncryptionConfiguration: %w", err)
	}

	converged, err := r.isKASConverged(ctx, log, hcp)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to check KAS convergence: %w", err)
	}

	switch {
	case role == secretencryption.TargetKeyAbsent:
		// Target key not yet in EncryptionConfiguration — CPO hasn't generated the new config yet.
		log.Info("Target key not yet in EncryptionConfiguration, waiting for CPO")
		r.setHistoryState(hcp, hyperv1.EncryptionMigrationStateReadOnlyDeploy)
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.ReadOnlyRolloutInProgressReason,
			Message:            "Waiting for new encryption key to be added to EncryptionConfiguration",
			ObservedGeneration: hcp.Generation,
		})
		return reconcile.Result{RequeueAfter: requeueWaitingForKAS}, nil

	case role == secretencryption.TargetKeyReadOnly && !converged:
		// Target key is read-only but KAS hasn't fully rolled out with it.
		log.Info("Target key is read-only in config, waiting for KAS convergence")
		r.setHistoryState(hcp, hyperv1.EncryptionMigrationStateReadOnlyDeploy)
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.ReEncryptionWaitingForKASReason,
			Message:            "Waiting for KAS to converge with new encryption key in read-only mode",
			ObservedGeneration: hcp.Generation,
		})
		return reconcile.Result{RequeueAfter: requeueWaitingForKAS}, nil

	case role == secretencryption.TargetKeyReadOnly && converged:
		// KAS converged with target key as read-only — ready for write promotion.
		log.Info("KAS converged with read-only key, ready for write promotion")
		r.setHistoryState(hcp, hyperv1.EncryptionMigrationStateWritePromote)
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.WritePromotionInProgressReason,
			Message:            "Promoting new encryption key to write provider",
			ObservedGeneration: hcp.Generation,
		})
		return reconcile.Result{}, nil

	case role == secretencryption.TargetKeyWrite && !converged:
		// Target key is the write provider but KAS hasn't converged with it.
		log.Info("Target key is write provider, waiting for KAS convergence")
		r.setHistoryState(hcp, hyperv1.EncryptionMigrationStateWritePromote)
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.ReEncryptionWaitingForKASReason,
			Message:            "Waiting for KAS to converge with new encryption key as write provider",
			ObservedGeneration: hcp.Generation,
		})
		return reconcile.Result{RequeueAfter: requeueWaitingForKAS}, nil

	case role == secretencryption.TargetKeyWrite && converged:
		// Target key is write provider and KAS converged — run migrations.
		log.Info("KAS converged with target key as write provider, running migrations")
		r.setHistoryState(hcp, hyperv1.EncryptionMigrationStateMigrating)
		return r.handleMigratingPhase(log, hcp)

	default:
		return reconcile.Result{}, nil
	}
}

// deriveTargetKeyRole reads the kas-secret-encryption-config Secret, parses the
// EncryptionConfiguration, and determines where the target key appears.
func (r *Reconciler) deriveTargetKeyRole(ctx context.Context, hcp *hyperv1.HostedControlPlane) (secretencryption.TargetKeyRole, error) {
	secret := &corev1.Secret{}
	secretKey := crclient.ObjectKey{
		Namespace: hcp.Namespace,
		Name:      manifests.KASSecretEncryptionConfigFile("").Name,
	}
	if err := r.cpClient.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return secretencryption.TargetKeyAbsent, nil
		}
		return secretencryption.TargetKeyAbsent, fmt.Errorf("failed to get encryption config secret: %w", err)
	}

	configBytes := secret.Data[secretencryption.EncryptionConfigurationKey]
	if len(configBytes) == 0 {
		return secretencryption.TargetKeyAbsent, nil
	}

	currentConfig, err := secretencryption.DecodeEncryptionConfiguration(configBytes)
	if err != nil {
		return secretencryption.TargetKeyAbsent, err
	}

	targetName, err := r.computeTargetKeyProviderName(ctx, hcp)
	if err != nil {
		return secretencryption.TargetKeyAbsent, fmt.Errorf("failed to compute target key provider name: %w", err)
	}

	return secretencryption.FindKeyRole(currentConfig, targetName, hcp.Spec.SecretEncryption.Type), nil
}

// computeTargetKeyProviderName computes the provider name that the CPO would
// generate for the target key in the EncryptionConfiguration. It reuses the
// same functions the CPO uses to ensure the names always match.
func (r *Reconciler) computeTargetKeyProviderName(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	tk := hcp.Status.SecretEncryption.TargetKey
	if tk.Provider == "" {
		return "", fmt.Errorf("no target key set")
	}

	switch tk.Provider {
	case hyperv1.SecretEncryptionProviderAzure:
		if tk.Azure.KeyVaultName == "" {
			return "", fmt.Errorf("azure target key status is nil")
		}
		return kms.AzureKMSProviderName(hyperv1.AzureKMSKey{
			KeyVaultName: tk.Azure.KeyVaultName,
			KeyName:      tk.Azure.KeyName,
			KeyVersion:   tk.Azure.KeyVersion,
		})

	case hyperv1.SecretEncryptionProviderAWS:
		if tk.AWS.ARN == "" {
			return "", fmt.Errorf("aws target key status is nil")
		}
		return kms.AWSKMSProviderName(tk.AWS.ARN)

	case hyperv1.SecretEncryptionProviderIBMCloud:
		// IBM Cloud uses a single KMS provider with a fixed name; the sidecar
		// handles key versioning internally via KP_DATA_JSON.
		return kms.IBMCloudKMSProviderName(), nil

	case hyperv1.SecretEncryptionProviderAESCBC:
		if tk.AESCBC.DataHash == "" {
			return "", fmt.Errorf("aescbc target key status is nil")
		}
		secret := &corev1.Secret{}
		if err := r.cpClient.Get(ctx, types.NamespacedName{
			Namespace: hcp.Namespace,
			Name:      tk.AESCBC.Secret.Name,
		}, secret); err != nil {
			return "", fmt.Errorf("failed to get AESCBC target key secret: %w", err)
		}
		return kasaescbc.AESCBCKeyName(secret.Data[hyperv1.AESCBCKeySecretKey])

	default:
		return "", fmt.Errorf("unsupported provider: %s", tk.Provider)
	}
}

// setHistoryState updates history[0].state for observability. This value is
// never used as input for phase derivation.
func (r *Reconciler) setHistoryState(hcp *hyperv1.HostedControlPlane, state hyperv1.EncryptionMigrationState) {
	if len(hcp.Status.SecretEncryption.History) > 0 {
		hcp.Status.SecretEncryption.History[0].State = state
	}
}

// handleMigratingPhase creates/monitors StorageVersionMigration CRs for each encrypted resource.
func (r *Reconciler) handleMigratingPhase(log logr.Logger, hcp *hyperv1.HostedControlPlane) (reconcile.Result, error) {
	if !r.migrator.HasSynced() {
		log.Info("Migrator cache not yet synced, requeuing")
		return reconcile.Result{RequeueAfter: requeueMigrationProgress}, nil
	}

	resources := r.encryptedResources(hcp)
	targetFingerprint := secretencryption.FingerprintFromKeyStatus(&hcp.Status.SecretEncryption.TargetKey)
	writeKey := fmt.Sprintf("encryption-key-%s", targetFingerprint)

	allFinished := true
	var migrationErrors []string

	for _, gr := range resources {
		finished, result, _, err := r.migrator.EnsureMigration(gr, writeKey)
		if err != nil {
			if strings.Contains(err.Error(), "failed to find version") {
				log.Info("Skipping migration for resource not found in discovery", "resource", gr.String())
				continue
			}
			return reconcile.Result{}, fmt.Errorf("failed to ensure migration for %s: %w", gr, err)
		}
		if !finished {
			allFinished = false
			continue
		}
		if result != nil {
			migrationErrors = append(migrationErrors, fmt.Sprintf("%s: %v", gr, result))
		}
	}

	if len(migrationErrors) > 0 {
		errMsg := strings.Join(migrationErrors, "; ")
		log.Info("StorageVersionMigration encountered errors", "errors", errMsg)
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.ReEncryptionFailedReason,
			Message:            fmt.Sprintf("Re-encryption failed for some resources: %s", errMsg),
			ObservedGeneration: hcp.Generation,
		})
		recordMigrationFailure(r.hcpNamespace, r.hcpName)
		return reconcile.Result{RequeueAfter: requeueMigrationFailure}, nil
	}

	if !allFinished {
		log.Info("StorageVersionMigrations still in progress")
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.ReEncryptionInProgressReason,
			Message:            "Re-encrypting etcd data with new encryption key",
			ObservedGeneration: hcp.Generation,
		})
		return reconcile.Result{RequeueAfter: requeueWaitingForKAS}, nil
	}

	// All migrations completed successfully.
	log.Info("All StorageVersionMigrations completed successfully")
	r.setHistoryState(hcp, hyperv1.EncryptionMigrationStateCompleted)
	if len(hcp.Status.SecretEncryption.History) > 0 {
		hcp.Status.SecretEncryption.History[0].CompletionTime = metav1.Time{Time: r.now()}
	}

	return r.completeRotation(log, hcp)
}

// completeRotation promotes the target key to active and clears the target key.
func (r *Reconciler) completeRotation(log logr.Logger, hcp *hyperv1.HostedControlPlane) (reconcile.Result, error) {
	if hcp.Status.SecretEncryption.TargetKey.Provider != "" {
		log.Info("Completing rotation: promoting target key to active")
		hcp.Status.SecretEncryption.ActiveKey = *hcp.Status.SecretEncryption.TargetKey.DeepCopy()
		hcp.Status.SecretEncryption.TargetKey = hyperv1.SecretEncryptionKeyStatus{}
	}

	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.ReEncryptionCompletedReason,
		Message:            "All etcd data is encrypted with the current active key",
		ObservedGeneration: hcp.Generation,
	})

	return reconcile.Result{}, nil
}

// isKASConverged checks if the KAS Deployment has fully rolled out.
func (r *Reconciler) isKASConverged(ctx context.Context, log logr.Logger, hcp *hyperv1.HostedControlPlane) (bool, error) {
	deployment := &appsv1.Deployment{}
	kasRef := manifests.KASDeployment(hcp.Namespace)
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(kasRef), deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get KAS deployment: %w", err)
	}

	ready := podspec.IsDeploymentReady(ctx, deployment)
	if !ready {
		log.V(2).Info("KAS not converged")
	}
	return ready, nil
}

// keyStatusFromSpec computes a SecretEncryptionKeyStatus from the HCP spec.
// For AESCBC, this reads the key secret to compute the data hash.
func (r *Reconciler) keyStatusFromSpec(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*hyperv1.SecretEncryptionKeyStatus, error) {
	spec := hcp.Spec.SecretEncryption
	if spec == nil {
		return nil, nil
	}

	var dataHash string
	if spec.Type == hyperv1.AESCBC && spec.AESCBC != nil {
		secret := &corev1.Secret{}
		secretKey := types.NamespacedName{
			Namespace: hcp.Namespace,
			Name:      spec.AESCBC.ActiveKey.Name,
		}
		if err := r.cpClient.Get(ctx, secretKey, secret); err != nil {
			return nil, fmt.Errorf("failed to get AESCBC key secret %s: %w", secretKey, err)
		}
		keyData, ok := secret.Data[hyperv1.AESCBCKeySecretKey]
		if !ok {
			return nil, fmt.Errorf("AESCBC key secret %s missing %q key", secretKey, hyperv1.AESCBCKeySecretKey)
		}
		dataHash = secretencryption.DataHash(keyData)
	}

	return secretencryption.KeyStatusFromSpec(spec, dataHash), nil
}

// encryptedResources returns the list of GroupResources that need re-encryption.
// Resources served by API servers that are not deployed (e.g. oauth resources
// when ExternalOIDC is configured) are excluded since they have no data to migrate.
func (r *Reconciler) encryptedResources(hcp *hyperv1.HostedControlPlane) []schema.GroupResource {
	spec := hcp.Spec.SecretEncryption
	if spec == nil {
		return nil
	}

	var resourceStrings []string
	switch spec.Type {
	case hyperv1.KMS:
		resourceStrings = config.KMSEncryptedObjects()
	case hyperv1.AESCBC:
		resourceStrings = config.AESCBCEncryptedObjects()
	default:
		return nil
	}

	oauthEnabled := util.HCPOAuthEnabled(hcp)
	resources := make([]schema.GroupResource, 0, len(resourceStrings))
	for _, rs := range resourceStrings {
		gr := parseGroupResource(rs)
		if !oauthEnabled && gr.Group == "oauth.openshift.io" {
			continue
		}
		resources = append(resources, gr)
	}
	return resources
}

// parseGroupResource converts a resource string like "routes.route.openshift.io" to a GroupResource.
func parseGroupResource(rs string) schema.GroupResource {
	parts := strings.SplitN(rs, ".", 2)
	if len(parts) == 1 {
		return schema.GroupResource{Resource: parts[0]}
	}
	return schema.GroupResource{Resource: parts[0], Group: parts[1]}
}

// prependHistory prepends a new entry to the history and trims it to maxHistoryEntries.
func prependHistory(history []hyperv1.EncryptionMigrationHistory, entry hyperv1.EncryptionMigrationHistory) []hyperv1.EncryptionMigrationHistory {
	result := make([]hyperv1.EncryptionMigrationHistory, 0, maxHistoryEntries)
	result = append(result, entry)
	for i, h := range history {
		if i >= maxHistoryEntries-1 {
			break
		}
		result = append(result, h)
	}
	return result
}
