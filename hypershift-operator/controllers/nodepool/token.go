package nodepool

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/backwardcompat"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/k8sutil"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
)

const (
	TokenSecretTokenGenerationTime       = "hypershift.openshift.io/last-token-generation-time"
	TokenSecretReleaseKey                = "release"
	TokenSecretReleaseVersionKey         = "release-version"
	TokenSecretTokenKey                  = "token"
	TokenSecretPullSecretHashKey         = "pull-secret-hash"
	TokenSecretHCConfigurationHashKey    = "hc-configuration-hash"
	TokenSecretAdditionalTrustBundleKey  = "additional-trust-bundle-hash"
	TokenSecretCloudConfigHashKey        = "cloud-config-hash"
	TokenSecretConfigKey                 = "config"
	TokenSecretAnnotation                = "hypershift.openshift.io/ignition-config"
	TokenSecretIgnitionReachedAnnotation = "hypershift.openshift.io/ignition-reached"
	TokenSecretNodePoolUpgradeType       = "hypershift.openshift.io/node-pool-upgrade-type"
	TokenSecretOSStreamKey               = "os-stream"
)

// Token knows how to create an UUUID token for a unique configGenerator Hash.
// It also knows how to manage the lifecycle of a corresponding token secret that it is used by the tokenSecret controller to generate the final ignition payload
// and a user data secret that points to the ignition server URL using the UUUID as an authenticator header to get that payload.
type Token struct {
	upsert.CreateOrUpdateProvider
	cpoCapabilities *CPOCapabilities
	*ConfigGenerator
	// TODO(alberto): we don't really support content inplace changes for fields like pull secret and AdditionalTrustBundle.
	// In fact we only trigger a rollout if the .Name referenced in the field changes.
	// Consider removing these hash checks and consolidate with the rolloutConfig struct input.
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	pullSecretHash            []byte
	additionalTrustBundleHash []byte
	globalConfigHash          []byte
	cloudConfigHash           []byte
	userData                  *userData

	// deployedBootstrapHash is the full hash extracted from the CAPI workload's
	// current bootstrap secret reference (MachineDeployment or MachineSet). Set
	// by the caller before Reconcile() so resolveEffectiveHash() can maintain
	// the in-progress rollout target during management-side drift.
	deployedBootstrapHash string

	// The workload version and rollout hash identify the request persisted with
	// its bootstrap reference, even if the subsequent NodePool patch failed.
	// Legacy workloads have no rollout hash; their version still distinguishes
	// an adopted rollout from a newer release request.
	deployedVersion       string
	deployedRolloutConfig string

	// effectiveHash is the full hash that this reconcile cycle is actually
	// maintaining secrets for. Set by resolveEffectiveHash() and used by
	// reconcileUserDataSecret() for the TargetConfigVersionHash ignition header.
	effectiveHash string

	// secretState records which state the secret maintenance state machine
	// resolved to during this reconcile cycle.
	secretState secretMaintenanceState
}

// userData contains the input necessary to generate the user data secret
// that points to the ignition server URL using the UUUID token as an authenticator header.
type userData struct {
	caCert                 []byte
	ignitionServerEndpoint string
	proxy                  *configv1.Proxy
}

// NewToken is the contract to create a new Token struct.
func NewToken(ctx context.Context, configGenerator *ConfigGenerator, cpoCapabilities *CPOCapabilities) (*Token, error) {
	if configGenerator == nil {
		return nil, fmt.Errorf("configGenerator can't be nil")
	}

	if cpoCapabilities == nil {
		return nil, fmt.Errorf("cpoCapabilities can't be nil")
	}

	// TODO(alberto): tempReconciler is a NodePoolReconciler used temporarily until getPullSecretBytes and getAdditionalTrustBundle are factored.
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	tempReconciler := &NodePoolReconciler{
		Client: configGenerator.Client,
	}
	pullSecretBytes, err := tempReconciler.getPullSecretBytes(ctx, configGenerator.hostedCluster)
	if err != nil {
		return nil, err
	}

	additionalTrustBundleCM := &corev1.ConfigMap{}
	additionalTrustBundle := ""
	if configGenerator.hostedCluster.Spec.AdditionalTrustBundle != nil {
		additionalTrustBundleCM, err = tempReconciler.getAdditionalTrustBundle(ctx, configGenerator.hostedCluster)
		if err != nil {
			return nil, err
		}
		additionalTrustBundle = additionalTrustBundleCM.Data["ca-bundle.crt"]
	}

	// TODO(alberto): This hash should be consolidated with configGenerator using globalConfigString as that is what configGenerator uses to create a configGenerator.Hash() and so what triggers a rollout.
	// This inconsistency was introduced by https://github.com/openshift/hypershift/pull/3795
	// See reconcileTokenSecret and https://github.com/openshift/hypershift/pull/4057 for more info on how this is used.
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.

	// Some fields in the ClusterConfiguration have changes that are not backwards compatible with older versions of the CPO.
	hcConfigurationHash, err := backwardcompat.GetBackwardCompatibleConfigHash(configGenerator.hostedCluster.Spec.Configuration)
	if err != nil {
		return nil, fmt.Errorf("failed to hash HostedCluster configuration: %w", err)
	}

	cloudConfigHash, err := configGenerator.GetCloudConfigHash(ctx)
	if err != nil {
		return nil, err
	}

	token := &Token{
		CreateOrUpdateProvider:    upsert.New(false),
		ConfigGenerator:           configGenerator,
		cpoCapabilities:           cpoCapabilities,
		pullSecretHash:            []byte(supportutil.HashSimple(pullSecretBytes)),
		additionalTrustBundleHash: []byte(supportutil.HashSimple(additionalTrustBundle)),
		globalConfigHash:          []byte(hcConfigurationHash),
		cloudConfigHash:           []byte(cloudConfigHash),
	}

	// User data input.
	caCert, err := token.getIgnitionCACert(ctx)
	if err != nil {
		return nil, err
	}

	ignEndpoint := configGenerator.hostedCluster.Status.IgnitionEndpoint
	if ignEndpoint == "" {
		return nil, fmt.Errorf("ignition endpoint is not set")
	}

	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(proxy, configGenerator.hostedCluster)

	token.userData = &userData{
		ignitionServerEndpoint: ignEndpoint,
		caCert:                 caCert,
		proxy:                  proxy,
	}

	return token, nil
}

// getInitionCACert gets the ignition CA cert from a secret.
// It's needed to generate a valid ignition config within the user data secret.
func (t *Token) getIgnitionCACert(ctx context.Context) ([]byte, error) {
	// Validate Ignition CA Secret.
	caSecret := ignitionserver.IgnitionCACertSecret(t.controlplaneNamespace)
	if err := t.Get(ctx, client.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		return nil, err
	}

	caCertBytes, hasCACert := caSecret.Data[corev1.TLSCertKey]
	if !hasCACert {
		return nil, fmt.Errorf("CA Secret is missing tls.crt key")
	}

	return caCertBytes, nil
}

// isOutdated returns true when a spec-driven change (version or config) requires
// new token and user-data secrets. Management-side-only changes (e.g. HAProxy image
// bumps) return false — existing secrets remain valid and the MachineDeployment
// continues to reference them.
func (t *Token) isOutdated() bool {
	if t.deployedVersion != "" && t.deployedVersion != t.Version() {
		return true
	}
	// Prefer the request persisted with the workload. NodePool tracking can be
	// missing or stale if its patch failed after the workload write succeeded.
	rolloutTarget := t.rolloutTargetHash()
	if rolloutTarget != "" && rolloutTarget != t.RolloutHash() {
		return true
	}
	currentRolloutConfig := t.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig]
	if currentRolloutConfig == "" {
		// Annotation absent: either a new NodePool (need to create secrets) or
		// an existing NodePool after operator upgrade (secrets already exist).
		if _, hasOldAnnotation := t.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]; hasOldAnnotation {
			// Migration policy: only version changes require new secrets.
			// Pre-migration config changes are absorbed into the new baseline
			// because the deployed full hash cannot distinguish user config
			// changes from management-only drift (e.g. HAProxy image bumps).
			// Once the rollout annotation is seeded, the standard rollout-hash
			// comparison handles future changes correctly.
			if t.Version() != t.nodePool.Status.Version {
				return true
			}
			return false
		}
		return true
	}
	versionChanged := t.Version() != t.nodePool.Status.Version
	configChanged := t.RolloutHashWithoutVersion() != currentRolloutConfig
	return versionChanged || configChanged
}

// rolloutTargetHash returns the request associated with the deployed payload.
// The workload annotation is saved atomically with the bootstrap reference and
// takes precedence over NodePool tracking, which is persisted separately.
// Fall back to NodePool tracking for workloads created before this annotation.
func (t *Token) rolloutTargetHash() string {
	if t.deployedRolloutConfig != "" {
		return t.deployedRolloutConfig
	}
	return t.nodePool.Annotations[nodePoolAnnotationInProgressRolloutConfig]
}

// referencedBootstrapDataSecretNames returns the set of user-data Secret names
// currently referenced as bootstrap data by this NodePool's machinery: the
// MachineDeployment/MachineSet bootstrap template and every in-flight Machine
// annotated with the NodePool annotation. This is the source of truth for
// whether an "outdated" Secret may be deleted: the cluster autoscaler can
// create new Machines referencing a Secret at any time, outside of any
// rollout, and provider machines (e.g. AzureMachine) only start provisioning
// when the referenced bootstrap data Secret still exists.
func (t *Token) referencedBootstrapDataSecretNames(ctx context.Context) (map[string]struct{}, error) {
	refs := map[string]struct{}{}
	nodePoolKey := client.ObjectKeyFromObject(t.nodePool).String()

	machineDeployment := &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: t.nodePool.GetName(), Namespace: t.controlplaneNamespace},
	}
	if err := t.Get(ctx, client.ObjectKeyFromObject(machineDeployment), machineDeployment); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get MachineDeployment: %w", err)
		}
	} else if name := ptr.Deref(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName, ""); name != "" {
		refs[name] = struct{}{}
	}

	// MachineSets are listed by the NodePool annotation rather than fetched by
	// name: during a rollout the MachineDeployment controller generates
	// MachineSets named <np>-<hash>-<suffix>, whose templates reference the
	// rollout target Secret. A rollout MachineSet with no Machines yet (e.g.
	// scaling up after a disruption) must keep its referenced Secret alive.
	machineSetList := &capiv1.MachineSetList{}
	if err := t.List(ctx, machineSetList, client.InNamespace(t.controlplaneNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list MachineSets: %w", err)
	}
	for i := range machineSetList.Items {
		machineSet := &machineSetList.Items[i]
		if machineSet.GetAnnotations()[nodePoolAnnotation] != nodePoolKey {
			continue
		}
		if name := ptr.Deref(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName, ""); name != "" {
			refs[name] = struct{}{}
		}
	}

	machineList := &capiv1.MachineList{}
	if err := t.List(ctx, machineList, client.InNamespace(t.controlplaneNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list Machines: %w", err)
	}
	for i := range machineList.Items {
		machine := &machineList.Items[i]
		if machine.GetAnnotations()[nodePoolAnnotation] != nodePoolKey {
			continue
		}
		if name := ptr.Deref(machine.Spec.Bootstrap.DataSecretName, ""); name != "" {
			refs[name] = struct{}{}
		}
	}
	return refs, nil
}

// repairMissingReferencedSecrets keeps bootstrap data Secrets that are still
// referenced by the MachineDeployment/MachineSet or by an in-flight Machine
// usable by Machines that have not booted yet. A provider machine whose
// bootstrap data Secret does not resolve never starts provisioning: it reports
// no error and no providerID, and silently hangs until the MachineHealthCheck
// nodeStartupTimeout fires. The same hang happens when the Secret exists but
// embeds a token the ignition server no longer serves: either its paired token
// Secret is gone, or the paired token Secret still exists but the ignition
// server rotated the token inside it. Re-created or refreshed Secrets keep the
// referenced (name-stable) name but carry the current configuration content
// and the current token, which is valid for any Machine that has not booted
// yet. Machines that already booted with the old content are unaffected.
func (t *Token) repairMissingReferencedSecrets(ctx context.Context, tokenBytes []byte) error {
	log := ctrl.LoggerFrom(ctx)

	referenced, err := t.referencedBootstrapDataSecretNames(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect bootstrap data Secret references: %w", err)
	}

	for referencedName := range referenced {
		// Only Secrets named exactly like this controller generates them for
		// this NodePool may be created or rewritten here: a Machine can
		// reference an arbitrarily named Secret as bootstrap data, including
		// one named for a different NodePool, and creating or overwriting such
		// a name with ignition data would hijack it for a different purpose.
		// The complete generated shape is validated, not just the prefix: a
		// prefix would also accept the Secrets of a NodePool whose name
		// extends this one's (e.g. "np" and "np-other").
		if !isNodePoolUserDataSecretName(referencedName, t.nodePool.GetName()) {
			continue
		}
		userDataSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      referencedName,
		}}
		err := t.Get(ctx, client.ObjectKeyFromObject(userDataSecret), userDataSecret)
		repairReason := ""
		switch {
		case apierrors.IsNotFound(err):
			// The referenced Secret is gone: the provider machine of any
			// Machine that has not booted yet can never start provisioning.
			repairReason = "missing"
		case err != nil:
			return fmt.Errorf("failed to get referenced user data Secret %q: %w", referencedName, err)
		default:
			// The Secret exists, but the bootstrap ignition config it carries
			// is only servable while the token it embeds is known to the
			// ignition server, which requires the paired token Secret
			// (token-<np>-<hash> <-> user-data-<np>-<hash>) to exist AND to
			// still carry the embedded token: the ignition server's token
			// controller rotates the token inside existing token Secrets, and
			// deletes them once expired. When the embedded token is no longer
			// served, Machines booting from this Secret keep receiving
			// "Token not found" and never join the cluster.
			pairedTokenSecret := &corev1.Secret{}
			pairedErr := t.Get(ctx, client.ObjectKey{Namespace: t.controlplaneNamespace, Name: pairedTokenSecretName(referencedName)}, pairedTokenSecret)
			switch {
			case apierrors.IsNotFound(pairedErr):
				repairReason = "paired token Secret is missing"
			case pairedErr != nil:
				return fmt.Errorf("failed to get paired token Secret for user data Secret %q: %w", referencedName, pairedErr)
			default:
				embedded, embedErr := embeddedBootstrapToken(userDataSecret)
				switch {
				case embedErr != nil:
					// Unparsable content cannot be served to a Machine either.
					repairReason = "ignition config is unparsable"
				case embedded != string(pairedTokenSecret.Data[TokenSecretTokenKey]) && embedded != string(tokenBytes):
					// The paired token Secret was rotated past the embedded
					// token. A Secret already carrying the current token (a
					// previous repair) is left alone.
					repairReason = "embedded token is no longer served"
				}
			}
		}
		if repairReason == "" {
			continue
		}
		// Refresh the Secret with the current token so Machines created from
		// it can bootstrap again. The upsert is idempotent (DeepEqual-guarded),
		// so repeating the repair on every reconciliation keeps a previously
		// repaired Secret in sync with later token rotations without writes.
		if result, err := t.CreateOrUpdate(ctx, t.Client, userDataSecret, func() error {
			return t.reconcileUserDataSecret(log, userDataSecret, string(tokenBytes))
		}); err != nil {
			return fmt.Errorf("failed to repair user data Secret %q: %w", referencedName, err)
		} else if result != controllerutil.OperationResultNone {
			log.Info("Repaired user data Secret", "secret", referencedName, "reason", repairReason, "result", result)
		}
	}
	return nil
}

func (t *Token) cleanupOutdated(ctx context.Context) error {
	currentRolloutConfig := t.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig]
	completedHash := t.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]
	// During migration (no rollout annotation), the "outdated" secrets are named
	// with the completed hash. If that hash matches the deployed bootstrap hash,
	// those secrets are still actively referenced by the CAPI workload. Skip
	// cleanup to avoid invalidating the active bootstrap reference before CAPI
	// propagation updates it to the new target.
	if currentRolloutConfig == "" && completedHash != "" && completedHash == t.deployedBootstrapHash {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)

	// A Secret being deleted is only truly "outdated" when nothing references
	// it. Machines created concurrently by the cluster autoscaler (which is not
	// a spec-driven rollout, so none of the rollout-control gates cover it) can
	// reference the Secret at the moment of deletion; when that happens the
	// provider machine never starts provisioning (no error, no providerID) and
	// the VM silently never boots. Check references before every deletion.
	referenced, err := t.referencedBootstrapDataSecretNames(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect bootstrap data Secret references: %w", err)
	}

	tokenSecret := t.outdatedTokenSecret()
	err = t.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get token Secret: %w", err)
	}
	if err == nil {
		// The token Secret is paired with the user data Secret of the same
		// config generation (token-<np>-<hash> <-> user-data-<np>-<hash>).
		// Keep the token valid while its paired user data Secret is still
		// referenced as bootstrap data, so Machines that have not booted yet
		// can still fetch their ignition payload once the expiry grace period
		// would have passed.
		pairedUserDataName := pairedUserDataSecretName(tokenSecret.Name)
		if _, isReferenced := referenced[pairedUserDataName]; isReferenced {
			// A referenced token must not stay marked as expired: the ignition
			// server's token controller deletes the Secret once the timestamp
			// passes, so a token expired by an earlier pass (while the paired
			// user data Secret was not yet referenced) would still disappear
			// and leave Machines booting from that Secret receiving
			// "Token not found".
			if err := clearExpirationTimestampOnToken(ctx, t.Client, tokenSecret); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to clear expiration on token Secret: %w", err)
			}
			log.Info("Skipping token Secret expiration: paired user data Secret is still referenced as bootstrap data",
				"secret", tokenSecret.Name)
		} else if err := setExpirationTimestampOnToken(ctx, t.Client, tokenSecret, nil); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to set expiration on token Secret: %w", err)
		}
	}

	// For AWS and KubeVirt, we keep the old userdata Secret so old Machines during rollout can be deleted.
	// AWS: deletion fails on CAPA < v2.2.0 (OCP < 4.16) because of
	// https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805.
	// TODO (Alberto): remove the AWS guard when OCP < 4.16 support is dropped.
	// KubeVirt: the Secret is shared by all VMs in the NodePool generation and must
	// survive until the rollout completes and all old VMs are gone. This is an
	// architectural requirement, not a temporary workaround.
	if t.nodePool.Spec.Platform.Type != hyperv1.AWSPlatform && t.nodePool.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		userDataSecret := t.outdatedUserDataSecret()
		err = t.Get(ctx, client.ObjectKeyFromObject(userDataSecret), userDataSecret)
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get user data Secret: %w", err)
		}
		if err == nil {
			// An outdated user data Secret must also not be deleted while it
			// is still referenced as bootstrap data: a Machine that has not
			// booted yet (e.g. one created by the cluster autoscaler, outside
			// of any rollout) can never start provisioning without it.
			if _, isReferenced := referenced[userDataSecret.Name]; isReferenced {
				log.Info("Skipping user data Secret deletion: still referenced as bootstrap data",
					"secret", userDataSecret.Name)
			} else if err := t.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete user data Secret: %w", err)
			} else if err == nil {
				ctrl.LoggerFrom(ctx).Info("Deleted outdated Secret", "secret", client.ObjectKeyFromObject(userDataSecret).String(), "nodePool", client.ObjectKeyFromObject(t.nodePool).String())
			}
		}
	}
	return nil
}

// clearExpirationTimestampOnToken removes the expiration annotation from a token
// Secret, so the ignition server's token controller stops treating it as expired
// and no longer deletes it. Used when an otherwise outdated token must be kept
// because its paired user data Secret is still referenced as bootstrap data.
func clearExpirationTimestampOnToken(ctx context.Context, c client.Client, tokenSecret *corev1.Secret) error {
	if _, hasExpirationTimestamp := tokenSecret.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation]; !hasExpirationTimestamp {
		return nil
	}

	delete(tokenSecret.Annotations, hyperv1.IgnitionServerTokenExpirationTimestampAnnotation)
	return c.Update(ctx, tokenSecret)
}

func setExpirationTimestampOnToken(ctx context.Context, c client.Client, tokenSecret *corev1.Secret, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}

	// there's no need to set the expiration timestamp annotation again if already set
	_, hasExpirationTimestamp := tokenSecret.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation]
	if hasExpirationTimestamp {
		return nil
	}

	// this should be a reasonable value to allow all in flight provisions to complete.
	timeUntilExpiry := 2 * time.Hour
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = map[string]string{}
	}
	tokenSecret.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation] = now().Add(timeUntilExpiry).Format(time.RFC3339)
	return c.Update(ctx, tokenSecret)
}

func (t *Token) Reconcile(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	outdated := t.isOutdated()
	if outdated {
		if err := t.cleanupOutdated(ctx); err != nil {
			return fmt.Errorf("failed to cleanup outdated token Secrets: %w", err)
		}
	}

	// Resolve which secrets to maintain based on the secret maintenance state
	// machine. When only management-side content changed, this returns the
	// deployed secrets (not new ones under the drifted hash) so that the
	// MachineDeployment's bootstrap reference stays valid and the ignition
	// server's token rotation doesn't invalidate the embedded token UUID.
	tokenSecret, userDataSecret := t.effectiveSecrets(outdated)

	if result, err := t.CreateOrUpdate(ctx, t.Client, tokenSecret, func() error {
		return t.reconcileTokenSecret(tokenSecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile token Secret: %w", err)
	} else {
		log.Info("Reconciled token Secret", "result", result)
	}

	tokenBytes, hasToken := tokenSecret.Data[TokenSecretTokenKey]
	if !hasToken {
		// This should never happen by design.
		return fmt.Errorf("token secret is missing token key")
	}

	if result, err := t.CreateOrUpdate(ctx, t.Client, userDataSecret, func() error {
		return t.reconcileUserDataSecret(log, userDataSecret, string(tokenBytes))
	}); err != nil {
		return err
	} else {
		log.Info("Reconciled user data Secret", "result", result)
	}

	// A bootstrap data Secret that is still referenced but has gone missing
	// (e.g. deleted while a Machine created outside of a rollout was starting
	// up) leaves the provider machine unable to provision. Re-create any
	// missing referenced Secret with the current configuration content.
	if err := t.repairMissingReferencedSecrets(ctx, tokenBytes); err != nil {
		return fmt.Errorf("failed to repair missing referenced user data Secrets: %w", err)
	}
	return nil
}

// SetDeployedBootstrapHash records the full hash extracted from the CAPI
// workload's current bootstrap secret reference (e.g. MachineDeployment or
// MachineSet). Must be called before Reconcile() so effectiveSecrets() can
// maintain the in-progress rollout target during management-side drift.
func (t *Token) SetDeployedBootstrapHash(hash string) {
	t.deployedBootstrapHash = hash
}

// EffectiveHash returns the full hash that this reconcile cycle maintained
// secrets for. Determined by resolveEffectiveHash() during Reconcile().
// Only valid after Reconcile() has been called.
func (t *Token) EffectiveHash() string {
	if t.effectiveHash == "" {
		return t.Hash()
	}
	return t.effectiveHash
}

// secretMaintenanceState represents the resolved state of the secret
// maintenance state machine. Each state determines which hash is used
// to name and maintain the token and user-data secrets.
//
// See docs/content/reference/nodepool-rollout-state-machine.md for the
// full state diagram and transition rules.
type secretMaintenanceState string

const (
	// stateSteady: no spec-driven change is pending, and the deployed and
	// completed hashes are absent or match Hash(). Maintain secrets under Hash().
	stateSteady secretMaintenanceState = "Steady"

	// stateManagementDrift: management-side content changed (e.g. HAProxy
	// image bump) but no spec-driven rollout is needed. The CAPI workload
	// still references the deployed bootstrap hash. Maintain those secrets
	// so token rotation stays in sync.
	stateManagementDrift secretMaintenanceState = "ManagementDrift"

	// stateCompletedDrift: the deployed hash is absent or matches Hash(),
	// but the completed hash differs without a spec-driven change.
	// Maintain the completed secrets.
	stateCompletedDrift secretMaintenanceState = "CompletedDrift"

	// stateNewRollout: a spec-driven change requires secrets under Hash(),
	// with no differing active target to retain or supersede. Also covers
	// a target already deployed but awaiting rollout completion.
	stateNewRollout secretMaintenanceState = "NewRollout"

	// stateAdoptedRollout: a mid-flight rollout predates the migration
	// to rollout annotations. The deployed hash differs from the completed
	// hash, indicating an active rollout target from the old operator.
	// Maintain those deployed secrets to keep the bootstrap reference alive.
	stateAdoptedRollout secretMaintenanceState = "AdoptedRollout"

	// stateContinuedRollout: the in-progress annotation matches the
	// current RolloutHash(), meaning the same rollout is still in flight
	// but management content has drifted. Continue maintaining the
	// deployed secrets.
	stateContinuedRollout secretMaintenanceState = "ContinuedRollout"

	// stateSupersededRollout: a genuinely new spec-driven change arrived
	// while a previous rollout was in flight. The in-progress annotation
	// does NOT match the current RolloutHash(). Switch to the new Hash().
	stateSupersededRollout secretMaintenanceState = "SupersededRollout"
)

// resolveEffectiveHash determines which full hash should be used to name and
// maintain the token and user-data secrets for this reconcile cycle. The
// decision is modeled as an explicit state machine with seven states — see
// the secretMaintenanceState constants for descriptions.
//
// Inputs:
//   - outdated: true when isOutdated() detected a spec-driven change
//   - t.deployedBootstrapHash: hash from the CAPI workload's current bootstrap ref
//   - t.deployedVersion: version persisted with the workload's bootstrap ref
//   - annotations: nodePoolAnnotationCurrentConfigVersion (completed hash),
//     rolloutTargetHash() (workload target, falling back to NodePool tracking)
//   - t.Hash(), t.RolloutHash(): current calculated hashes
//
// The resolved state is stored in t.secretState.
func (t *Token) resolveEffectiveHash(outdated bool) secretMaintenanceState {
	completedHash := t.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]
	deployedHash := t.deployedBootstrapHash
	inProgressRollout := t.rolloutTargetHash()
	currentHash := t.Hash()

	deployedDiffers := deployedHash != "" && deployedHash != currentHash

	if !outdated {
		// No spec-driven change. Maintain whichever hash is currently active.
		switch {
		case deployedDiffers:
			// The CAPI workload references a different hash than what we'd
			// calculate now. This is management-side drift — the workload's
			// secrets must stay alive for token rotation.
			t.effectiveHash = deployedHash
			t.secretState = stateManagementDrift
		case completedHash != "" && completedHash != currentHash:
			// No CAPI workload (or its ref matches current), but the
			// completed annotation tracks a different hash. Maintain those.
			t.effectiveHash = completedHash
			t.secretState = stateCompletedDrift
		default:
			t.effectiveHash = currentHash
			t.secretState = stateSteady
		}
		return t.secretState
	}

	// A spec-driven change was detected (outdated == true).

	if deployedDiffers {
		if t.deployedVersion != "" && t.deployedVersion != t.Version() {
			// A legacy rollout may already be active when a newer release is
			// requested. Its payload must not be adopted for the new version.
			// This also handles reversions to the last completed version.
			t.effectiveHash = currentHash
			t.secretState = stateNewRollout
			if deployedHash != completedHash {
				t.secretState = stateSupersededRollout
			}
			return t.secretState
		}
		// The CAPI workload references a hash that differs from our target.
		// Determine whether this is a pre-existing rollout to adopt, the
		// same rollout with management drift, or a genuinely new request.

		// Check whether rollout annotations need to be considered: either the
		// deployed hash differs from the completed one (CAPI is ahead of
		// completion tracking) or a version mismatch exists. The latter also
		// covers a fresh version upgrade before any target has been propagated.
		rolloutInProgress := deployedHash != completedHash ||
			t.Version() != t.nodePool.Status.Version

		if rolloutInProgress {
			switch {
			case inProgressRollout == "" && deployedHash != completedHash:
				// Active rollout from before migration — the deployed hash
				// was the old operator's target. Keep those secrets alive.
				t.effectiveHash = deployedHash
				t.secretState = stateAdoptedRollout

			case inProgressRollout == "":
				// Deployed matches completed and no prior rollout is recorded.
				// The version mismatch signals a fresh upgrade.
				t.effectiveHash = currentHash
				t.secretState = stateNewRollout

			case inProgressRollout != "" && t.RolloutHash() == inProgressRollout:
				// Same rollout still in flight with management-only drift.
				t.effectiveHash = deployedHash
				t.secretState = stateContinuedRollout

			default:
				// Genuinely new spec-driven change superseding any in-flight
				// work. Switch to the new hash.
				t.effectiveHash = currentHash
				t.secretState = stateSupersededRollout
			}
			return t.secretState
		}
	}

	// Default: no deployed hash, no in-flight complexity, or the deployed
	// hash matches the current one (possibly awaiting rollout completion).
	t.effectiveHash = currentHash
	t.secretState = stateNewRollout
	return t.secretState
}

// effectiveSecrets returns the token and user data secrets that Reconcile
// should maintain, based on the state resolved by resolveEffectiveHash.
func (t *Token) effectiveSecrets(outdated bool) (*corev1.Secret, *corev1.Secret) {
	t.resolveEffectiveHash(outdated)
	if t.effectiveHash == t.Hash() {
		return t.TokenSecret(), t.UserDataSecret()
	}
	return t.secretsForHash(t.effectiveHash)
}

func (t *Token) secretsForHash(hash string) (*corev1.Secret, *corev1.Secret) {
	return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.controlplaneNamespace,
				Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, t.nodePool.GetName(), hash),
			},
		}, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.controlplaneNamespace,
				Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, t.nodePool.GetName(), hash),
			},
		}
}

const UserDataSecrePrefix = "user-data"

// pairedTokenSecretName maps a user data Secret name to the name of the token
// Secret of the same config generation (user-data-<np>-<hash> <->
// token-<np>-<hash>).
func pairedTokenSecretName(userDataSecretName string) string {
	return TokenSecretPrefix + strings.TrimPrefix(userDataSecretName, UserDataSecrePrefix)
}

// isNodePoolUserDataSecretName reports whether name is exactly the generated
// name of one of nodePoolName's user data Secrets: user-data-<np>-<hash>,
// where <hash> is the eight lowercase hexadecimal characters produced by
// support/util.HashSimple. Validating the complete shape matters: a prefix
// match would also accept the Secrets of a NodePool whose name extends this
// one's (e.g. "np" and "np-other"), which repair must never write.
func isNodePoolUserDataSecretName(name, nodePoolName string) bool {
	hash, ok := strings.CutPrefix(name, UserDataSecrePrefix+"-"+nodePoolName+"-")
	if !ok || len(hash) != 8 {
		return false
	}
	for i := 0; i < len(hash); i++ {
		c := hash[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (t *Token) UserDataSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, t.ConfigGenerator.nodePool.GetName(), t.ConfigGenerator.Hash()),
		},
	}
}

func (t *Token) outdatedUserDataSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, t.ConfigGenerator.nodePool.GetName(), t.nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion]),
		},
	}
}

const TokenSecretPrefix = "token"

// pairedUserDataSecretName maps a token Secret name to the name of the user
// data Secret of the same config generation (token-<np>-<hash> <->
// user-data-<np>-<hash>).
func pairedUserDataSecretName(tokenSecretName string) string {
	return UserDataSecrePrefix + strings.TrimPrefix(tokenSecretName, TokenSecretPrefix)
}

func (t *Token) TokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, t.ConfigGenerator.nodePool.GetName(), t.ConfigGenerator.Hash()),
		},
	}
}

func (t *Token) outdatedTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, t.ConfigGenerator.nodePool.GetName(), t.nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion]),
		},
	}
}

func (t *Token) reconcileTokenSecret(tokenSecret *corev1.Secret) error {
	// The token secret controller updates expired token IDs for token Secrets.
	// When that happens the NodePool controller reconciles the userData Secret with the new token ID.
	// Therefore, this secret is mutable.
	tokenSecret.Immutable = ptr.To(false)
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = make(map[string]string)
	}

	tokenSecret.Annotations[TokenSecretAnnotation] = "true"
	tokenSecret.Annotations[TokenSecretNodePoolUpgradeType] = string(t.nodePool.Spec.Management.UpgradeType)
	tokenSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(t.nodePool).String()
	if karpenterutil.IsKarpenterEnabled(t.hostedCluster.Spec.AutoNode) {
		npLabels := t.nodePool.GetLabels()
		if npLabels != nil && npLabels[karpenterutil.ManagedByKarpenterLabel] == "true" {
			tokenSecret.Annotations[k8sutil.HostedClusterAnnotation] = client.ObjectKeyFromObject(t.ConfigGenerator.hostedCluster).String()
			if tokenSecret.Labels == nil {
				tokenSecret.Labels = make(map[string]string)
			}
			tokenSecret.Labels[karpenterutil.ManagedByKarpenterLabel] = "true"
		}
	}
	// active token should never be marked as expired.
	delete(tokenSecret.Annotations, hyperv1.IgnitionServerTokenExpirationTimestampAnnotation)

	// During a backup/restore the token secret may be deleted by the secret janitor
	// before the NodePool is restored, causing the NodePool controller to create a new
	// token secret without the ignition-reached annotation. Since the nodes are already
	// running and won't contact the ignition endpoint again, the annotation must be
	// carried over so that ReachedIgnitionEndpoint remains True and MachineHealthChecks
	// continue to be created.
	if _, restored := t.hostedCluster.Annotations[hyperv1.HostedClusterRestoredFromBackupAnnotation]; restored {
		tokenSecret.Annotations[TokenSecretIgnitionReachedAnnotation] = "True"
	}

	if tokenSecret.Data == nil {
		// 2. - Reconcile towards expected state of the world.
		compressedConfig, err := t.CompressedAndEncoded()
		if err != nil {
			return fmt.Errorf("failed to compress and decode config: %w", err)
		}

		// TODO (alberto): Drop this after dropping < 4.12 support.
		// So all CPOs ign server will know to decompress and decode.
		if !t.cpoCapabilities.DecompressAndDecodeConfig {
			compressedConfig, err = t.Compressed()
			if err != nil {
				return fmt.Errorf("failed to compress config: %w", err)
			}
		}

		tokenSecret.Data = map[string][]byte{}
		tokenSecret.Annotations[TokenSecretTokenGenerationTime] = time.Now().Format(time.RFC3339Nano)
		tokenSecret.Data[TokenSecretTokenKey] = []byte(uuid.New().String())
		tokenSecret.Data[TokenSecretReleaseKey] = []byte(t.nodePool.Spec.Release.Image)
		tokenSecret.Data[TokenSecretReleaseVersionKey] = []byte(t.releaseImage.Version())
		tokenSecret.Data[TokenSecretConfigKey] = compressedConfig.Bytes()

		// Hash values that are used by the "token secret controller" / "local ignition provider"  to determine if this input
		// have changed before generating a payload for it.
		tokenSecret.Data[TokenSecretPullSecretHashKey] = t.pullSecretHash
		tokenSecret.Data[TokenSecretAdditionalTrustBundleKey] = t.additionalTrustBundleHash
		tokenSecret.Data[TokenSecretHCConfigurationHashKey] = t.globalConfigHash
		tokenSecret.Data[TokenSecretOSStreamKey] = []byte(t.resolvedRHELStreamForBootImage)
		tokenSecret.Data[TokenSecretCloudConfigHashKey] = t.cloudConfigHash
	}
	// TODO (alberto): Only apply this on creation and change the hash generation to only use triggering upgrade fields.
	// We let this change to happen inplace now as the tokenSecret and the mcs config use the whole spec.Config for the comparing hash.
	// Otherwise if something which does not trigger a new token generation from spec.Config changes, like .IDP, both hashes would mismatch forever.
	tokenSecret.Data[TokenSecretHCConfigurationHashKey] = t.globalConfigHash
	tokenSecret.Data[TokenSecretCloudConfigHashKey] = t.cloudConfigHash

	return nil
}

func (t *Token) reconcileUserDataSecret(log logr.Logger, userDataSecret *corev1.Secret, token string) error {
	// The token secret controller deletes expired token Secrets.
	// When that happens the NodePool controller reconciles and create a new one.
	// Then it reconciles the userData Secret with the new generated token.
	// Therefore, this secret is mutable.
	userDataSecret.Immutable = ptr.To(false)

	if userDataSecret.Annotations == nil {
		userDataSecret.Annotations = make(map[string]string)
	}
	userDataSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(t.nodePool).String()
	if userDataSecret.Labels == nil {
		userDataSecret.Labels = make(map[string]string)
	}

	if karpenterutil.IsKarpenterEnabled(t.hostedCluster.Spec.AutoNode) {
		npLabels := t.nodePool.GetLabels()
		if npLabels != nil && npLabels[karpenterutil.ManagedByKarpenterLabel] == "true" {
			// TODO(maxcao13): Add support for userData reconciliation for Azure.
			// tracked in https://redhat.atlassian.net/browse/AUTOSCALE-970
			if t.hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
				err := setKarpenterAMILabels(log, userDataSecret, t.hostedCluster.Spec.Platform.AWS.Region, t.releaseImage, t.hostedCluster.Spec.Platform.Type, t.resolvedRHELStreamForBootImage)
				if err != nil {
					return err
				}
				userDataSecret.Labels[karpenterutil.ManagedByKarpenterLabel] = "true"
			} else {
				return fmt.Errorf("karpenter userData reconciliation is currently not supported for platform: %s", t.hostedCluster.Spec.Platform.Type)
			}
		}
	}

	encodedCACert := base64.StdEncoding.EncodeToString(t.userData.caCert)
	encodedToken := base64.StdEncoding.EncodeToString([]byte(token))
	ignConfig := ignConfig(encodedCACert, encodedToken, t.userData.ignitionServerEndpoint, t.EffectiveHash(), t.userData.proxy, t.nodePool)
	userDataValue, err := json.Marshal(ignConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal ignition config: %w", err)
	}
	userDataSecret.Data = map[string][]byte{
		"disableTemplating": []byte(base64.StdEncoding.EncodeToString([]byte("true"))),
		"value":             userDataValue,
	}
	return nil
}

func setKarpenterAMILabels(log logr.Logger, userDataSecret *corev1.Secret, region string, releaseImage *releaseinfo.ReleaseImage, platform hyperv1.PlatformType, rhelStream string) error {
	supportedArchitectures, err := karpenterutil.SupportedArchitectures(platform)
	if err != nil {
		return fmt.Errorf("failed to get supported architectures: %w", err)
	}
	supported := 0
	for _, arch := range supportedArchitectures {
		ami, err := defaultNodePoolAMI(region, arch, rhelStream, releaseImage)
		if err != nil {
			// skip unavailable architectures gracefully
			log.Error(err, "failed to get default NodePool AMI for architecture", "architecture", arch)
			continue
		}
		labelKey := karpenterutil.ArchToAMILabelKey(arch)
		userDataSecret.Labels[labelKey] = ami
		supported++
	}
	if supported == 0 {
		return fmt.Errorf("no supported architectures found")
	}
	return nil
}

func ignConfig(encodedCACert, encodedToken, endpoint, targetConfigVersionHash string, proxy *configv1.Proxy, nodePool *hyperv1.NodePool) ignitionapi.Config {
	cfg := ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
			Security: ignitionapi.Security{
				TLS: ignitionapi.TLS{
					CertificateAuthorities: []ignitionapi.Resource{
						{
							Source: ptr.To(fmt.Sprintf("data:text/plain;base64,%s", encodedCACert)),
						},
					},
				},
			},
			Config: ignitionapi.IgnitionConfig{
				Merge: []ignitionapi.Resource{
					{
						Source: ptr.To(fmt.Sprintf("https://%s/ignition", endpoint)),
						HTTPHeaders: []ignitionapi.HTTPHeader{
							{
								Name:  "Authorization",
								Value: ptr.To(fmt.Sprintf("Bearer %s", encodedToken)),
							},
							{
								Name:  "NodePool",
								Value: ptr.To(client.ObjectKeyFromObject(nodePool).String()),
							},
							{
								Name:  "TargetConfigVersionHash",
								Value: ptr.To(targetConfigVersionHash),
							},
						},
					},
				},
			},
		},
	}
	if proxy.Status.HTTPProxy != "" {
		cfg.Ignition.Proxy.HTTPProxy = ptr.To(proxy.Status.HTTPProxy)
	}
	if proxy.Status.HTTPSProxy != "" {
		cfg.Ignition.Proxy.HTTPSProxy = ptr.To(proxy.Status.HTTPSProxy)
	}
	if proxy.Status.NoProxy != "" {
		for _, item := range strings.Split(proxy.Status.NoProxy, ",") {
			cfg.Ignition.Proxy.NoProxy = append(cfg.Ignition.Proxy.NoProxy, ignitionapi.NoProxyItem(item))
		}
	}
	return cfg
}

// embeddedBootstrapToken returns the ignition-server token embedded in a user
// data Secret, from the Authorization header of the ignition config it
// carries. It errors when the Secret's payload cannot be parsed as one of our
// ignition configs or carries no token.
func embeddedBootstrapToken(userDataSecret *corev1.Secret) (string, error) {
	var config ignitionapi.Config
	if err := json.Unmarshal(userDataSecret.Data["value"], &config); err != nil {
		return "", fmt.Errorf("failed to unmarshal ignition config: %w", err)
	}
	for _, source := range config.Ignition.Config.Merge {
		for _, header := range source.HTTPHeaders {
			if header.Name != "Authorization" || header.Value == nil {
				continue
			}
			token, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(*header.Value, "Bearer "))
			if err != nil {
				return "", fmt.Errorf("failed to decode embedded token: %w", err)
			}
			return string(token), nil
		}
	}
	return "", fmt.Errorf("no Authorization header found in ignition config")
}
