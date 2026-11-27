package ignitionpayload

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/backwardcompat"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	"github.com/openshift/hypershift/support/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/statuspatching"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// storeCleanupFinalizer is removed only after every PayloadStore entry for the CR has been
	// freed. It keeps store reclamation consumer-agnostic (the consumer owns its own finalizer
	// separately), so a Karpenter-created CR's entries are freed even though Karpenter, not the
	// NodePool controller, drives its deletion.
	storeCleanupFinalizer = "hypershift.openshift.io/ignition-payload-store-cleanup"

	// trustBundleKey is the ConfigMap data key holding the additional trust bundle, matching the
	// key GetPayload reads and hashes.
	trustBundleKey = "ca-bundle.crt"

	// defaultTokenTTL is how long a minted bearer token is valid. The re-issue timer re-mints at half
	// the TTL so a future CAPI instance created hours after the rollout still boots with a valid token.
	// CAPI bakes the token into an instance at creation, so the token can already be up to TTL/2 old
	// then; the instance's creation→first-fetch budget is therefore at least TTL/2 (6h), in line with
	// the legacy ignition-server's rotation (11h TTL, rotated at half). An instance that misses the
	// budget gets a terminal 401 and must be replaced (it cannot be re-issued bootstrap data).
	defaultTokenTTL = 12 * time.Hour
)

// Reconciler generates ignition payloads for IgnitionPayload CRs: it reads the config the CR names,
// validates it, computes the payload-identity and rollout hashes, renders+stores payloads keyed by
// rollout identity (owner + rolloutGeneration), authors the pointer-ignition userData Secret, and
// advances status to drive rollouts.
type Reconciler struct {
	client.Client
	// APIReader is an uncached reader. Reconcile reads the CR through it once, so every decision —
	// the reclaim keep-set and the next rolloutGeneration — is made on committed status, never on an
	// informer that has not yet observed this controller's last commit.
	APIReader client.Reader
	Store     payloadstore.PayloadStore
	Generator *payloadGenerator
	Minter    TokenMinter
	// ReleaseProvider resolves a release-image pullspec to its OCP version for the rollout/identity
	// hashes (matching the deployed ConfigGenerator.Hash, which keys on releaseImage.Version()).
	ReleaseProvider releaseinfo.Provider
	Namespace       string

	// IgnitionCACert is the node-facing CA (PEM) embedded in authored userData so a booting node
	// trusts the serving tier. IgnitionServerEndpoint is the host[:port] the node fetches from.
	// Both are HCP-level facts injected at startup.
	IgnitionCACert         []byte
	IgnitionServerEndpoint string

	// TokenTTL bounds a minted token's validity; the userData token is re-issued once past half of
	// it. Defaults to defaultTokenTTL when zero.
	TokenTTL time.Duration

	// now is injectable for tests; defaults to time.Now.
	now func() time.Time
}

// ReconcilerConfig carries the dependencies the ignition-payload-controller wires into a Reconciler.
type ReconcilerConfig struct {
	Client client.Client
	// APIReader is an uncached reader (mgr.GetAPIReader()); see Reconciler.APIReader.
	APIReader              client.Reader
	Store                  payloadstore.PayloadStore
	Provider               ignserver.IgnitionProvider
	ReleaseProvider        releaseinfo.Provider
	Minter                 TokenMinter
	Namespace              string
	IgnitionCACert         []byte
	IgnitionServerEndpoint string
	TokenTTL               time.Duration
}

// NewReconciler builds a payload-controller Reconciler from its dependencies. It is the exported
// entry point for the ignition-payload-controller command.
func NewReconciler(cfg ReconcilerConfig) *Reconciler {
	return &Reconciler{
		Client:                 cfg.Client,
		APIReader:              cfg.APIReader,
		Store:                  cfg.Store,
		Generator:              &payloadGenerator{store: cfg.Store, provider: cfg.Provider},
		Minter:                 cfg.Minter,
		ReleaseProvider:        cfg.ReleaseProvider,
		Namespace:              cfg.Namespace,
		IgnitionCACert:         cfg.IgnitionCACert,
		IgnitionServerEndpoint: cfg.IgnitionServerEndpoint,
		TokenTTL:               cfg.TokenTTL,
	}
}

// Reconcile runs in a fixed order:
//
//  1. Read the CR uncached (APIReader), so every decision below is made on committed status.
//  2. Teardown / finalizer.
//  3. Reclaim, independent of the generation inputs: sweep everything committed status no longer
//     names (sweepOrphans), then commit retirement of a drained previous. Reclaim never waits on a
//     healthy config, and a generation is freed only by a reconcile whose authoritative read no longer
//     names it — so a just-uncommitted (evicted or retired) generation is freed by the next reconcile,
//     which the commit's own watch event drives.
//  4. Read inputs, validate, hash. A failure stops here.
//  5. Rebaseline, rollout (prepare -> commit), or refresh (repair -> commit).
//
// Every status commit goes through commitStatus, which guards PatchStatus's cached re-read against
// the generation this reconcile decided on.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cr := &hyperv1.IgnitionPayload{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	owner := payloadstore.OwnerRef{Namespace: cr.Namespace, Name: cr.Name}

	// Teardown: free every remaining store entry for the CR, then drop the finalizer. The authored
	// userData Secrets are CR-owned and cascade-delete once the CR is gone.
	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cr, storeCleanupFinalizer) {
			if err := r.freeAllGenerations(ctx, owner); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(cr, storeCleanupFinalizer)
			if err := r.Update(ctx, cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the store-cleanup finalizer is present before we write anything to the store.
	if !controllerutil.ContainsFinalizer(cr, storeCleanupFinalizer) {
		controllerutil.AddFinalizer(cr, storeCleanupFinalizer)
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
	}

	// committedGen is the status.current generation this reconcile decides on; commitStatus refuses to
	// apply a mutation against any other.
	committedGen := cr.Status.Current.RolloutGeneration
	// observedGen is the spec generation this reconcile processes, recorded as PayloadGenerated's
	// observedGeneration so consumers can tell whether status.current reflects their latest spec. It
	// is captured here, not read from cr inside a status commit: PatchStatus re-reads cr, and the
	// re-read may carry a newer spec this reconcile never rendered.
	observedGen := cr.Generation

	// Reclaim first, before (and regardless of) the generation inputs.
	if err := r.sweepOrphans(ctx, owner, cr); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.retirePrevious(ctx, cr, committedGen); err != nil {
		return ctrl.Result{}, err
	}

	// Read the credential/global-config inputs the CR names. Read failures are transient (the
	// referenced object may not exist yet) and requeue.
	inputs, err := r.readInputs(ctx, cr)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Validation gate: a manifest that fails defaulting/validation sets PayloadGenerated=False and
	// stops (no hash advance, no token, no rollout). A transient read error (e.g. a ConfigMap that
	// is briefly absent) is NOT a validation failure and requeues instead.
	resolved, err := resolveAndValidate(ctx, r.Client, cr)
	if err != nil {
		var ve *validationError
		if errors.As(err, &ve) {
			return ctrl.Result{}, statuspatching.PatchStatusCondition(ctx, r.Client, cr, &cr.Status.Conditions, metav1.Condition{
				Type:               hyperv1.IgnitionPayloadPayloadGeneratedCondition,
				Status:             metav1.ConditionFalse,
				Reason:             "InvalidConfig",
				Message:            err.Error(),
				ObservedGeneration: observedGen,
			})
		}
		return ctrl.Result{}, err
	}

	// Resolve the release pullspec to its OCP version for the hashes, matching the deployed
	// ConfigGenerator.Hash() which keys on releaseImage.Version() (not the pullspec). A Lookup failure
	// is transient and requeues; it is not a config-validation failure.
	releaseImage, err := r.ReleaseProvider.Lookup(ctx, cr.Spec.ReleaseImage, inputs.pullSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to resolve release image %q: %w", cr.Spec.ReleaseImage, err)
	}

	// The full cluster-configuration gate hash (what CPO writes into the machine-config-server
	// ConfigMap) and the cloud-provider config hash come from the HostedControlPlane in this
	// namespace, matching exactly what CPO renders from so GetPayload's freshness gate passes. A
	// missing HCP is transient.
	hcp, err := r.getHostedControlPlane(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	hcConfigHash, err := backwardcompat.GetBackwardCompatibleConfigHash(hcp.Spec.Configuration)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to hash hosted cluster configuration: %w", err)
	}
	cloudConfigHash, err := r.cloudConfigHash(ctx, hcp)
	if err != nil {
		return ctrl.Result{}, err
	}

	in := hashInputs{
		resolved:                resolved,
		releaseVersion:          releaseImage.Version(),
		pullSecretName:          cr.Spec.PullSecret.Name,
		pullSecretContent:       inputs.pullSecret,
		trustBundleName:         cr.Spec.AdditionalTrustBundle.Name,
		trustBundleContent:      inputs.trustBundle,
		rolloutGlobalConfig:     inputs.rolloutGlobalConfig,
		osImageStream:           cr.Spec.OSImageStream.Name,
		additionalRolloutInputs: cr.Spec.AdditionalRolloutInputs,
		hcConfigHash:            hcConfigHash,
		cloudConfigHash:         cloudConfigHash,
	}
	identity := payloadIdentityHash(in)
	ro := rolloutHash(in)

	gen := genInputs{
		releaseImage:   cr.Spec.ReleaseImage,
		customConfig:   joinManifests(resolved),
		pullSecretHash: supportutil.HashSimple(inputs.pullSecret),
		// HashSimple formats its argument with %v, so it is type-sensitive: HashSimple([]byte(nil))
		// hashes "[]" while HashSimple("") hashes "". The MCO provider (LocalIgnitionProvider.GetPayload)
		// validates this against HashSimple(string(...)) of the fetched trust bundle — matching the
		// legacy token path, which hashed a string. Hash the string form so the two agree; otherwise an
		// empty (or any) trust bundle never matches and generation fails with "additionalTrustBundle
		// does not match hash". The pull secret is []byte on both sides, so it needs no conversion.
		trustBundleHash:     supportutil.HashSimple(string(inputs.trustBundle)),
		hcConfigurationHash: hcConfigHash,
		osImageStream:       cr.Spec.OSImageStream.Name,
		cloudConfigHash:     cloudConfigHash,
	}

	// Rebaseline: if the stored formula version is older than the binary's, rewrite the stored
	// rollout hash to the new-formula value WITHOUT advancing rolloutGeneration. Serving identity
	// (rolloutGeneration, the store entry, the userData Secret, the node header) is keyed on the
	// generation, not the hash, so the rebaseline never orphans a live payload.
	if cr.Status.Current.RolloutGeneration > 0 && cr.Status.RolloutHashVersion < rolloutHashFormulaVersion {
		err := r.commitStatus(ctx, cr, committedGen, func() error {
			cr.Status.Current.RolloutHash = ro
			cr.Status.RolloutHashVersion = rolloutHashFormulaVersion
			return nil
		})
		return ctrl.Result{RequeueAfter: r.tokenRefreshInterval()}, err
	}

	if cr.Status.Current.RolloutGeneration == 0 || ro != cr.Status.Current.RolloutHash {
		return r.rollout(ctx, cr, owner, committedGen, observedGen, identity, ro, gen)
	}
	return r.refresh(ctx, cr, owner, committedGen, observedGen, identity, gen)
}

// commitStatus applies mutate through statuspatching.PatchStatus, whose re-read is served from the
// cache. The mutation is only valid against the status this reconcile decided on, identified by
// status.current == committedGen (read through the APIReader):
//
//   - re-read behind committedGen: the informer has not yet observed a commit the API has. Return a
//     Conflict so PatchStatus re-reads and retries. Mutating the stale object is unsafe even though a
//     changed patch would itself conflict: a mutation that is a no-op against the stale object makes
//     PatchStatus skip the write entirely, silently dropping the commit.
//   - re-read ahead of committedGen: another writer (e.g. an overlapping leader) committed since the
//     authoritative read. Abort; the next reconcile re-decides on the newer status.
func (r *Reconciler) commitStatus(ctx context.Context, cr *hyperv1.IgnitionPayload, committedGen int64, mutate func() error) error {
	return statuspatching.PatchStatus(ctx, r.Client, cr, func() error {
		switch got := cr.Status.Current.RolloutGeneration; {
		case got < committedGen:
			return apierrors.NewConflict(hyperv1.GroupVersion.WithResource("ignitionpayloads").GroupResource(), cr.Name,
				fmt.Errorf("cached status.current is generation %d, behind committed generation %d", got, committedGen))
		case got > committedGen:
			return fmt.Errorf("status.current advanced to generation %d concurrently (expected %d)", got, committedGen)
		}
		return mutate()
	})
}

// rollout advances rolloutGeneration as a prepare -> commit sequence, so no failure at any step leaves
// status naming a generation whose payload or userData Secret is gone:
//
//   - prepare: generate+store the new generation and author its userData Secret. Both are idempotent
//     and invisible until committed, so a render failure leaves current and previous untouched and
//     still servable (nodes booting on previous keep being served, and the draining MachineSet's
//     userData Secret survives).
//   - commit: a single guarded, optimistic-locked status patch moves current->previous, sets the new
//     current, and resets IgnitionReached. The guard (commitStatus) ensures newGen is still
//     committedGen+1, so a generation number is never bound to two rollout identities.
//
// The evicted old previous is no longer advertised once committed (at most two generations are, even
// when rollouts overtake each other); the next reconcile's sweepOrphans frees it.
func (r *Reconciler) rollout(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef, committedGen, observedGen int64, identity, ro string, gen genInputs) (ctrl.Result, error) {
	newGen := committedGen + 1
	// prev is the generation this rollout supersedes; captured by value before the status patch
	// re-fetches cr, so it survives an optimistic-lock retry (commitStatus guarantees the re-read
	// still has it as current).
	prev := cr.Status.Current

	if _, err := r.Generator.ensurePayload(ctx, owner, newGen, identity, gen); err != nil {
		return ctrl.Result{}, err
	}
	userDataName, err := r.ensureUserDataSecret(ctx, cr, owner, newGen)
	if err != nil {
		return ctrl.Result{}, err
	}
	current := hyperv1.PayloadReference{
		ConfigHash:        identity,
		RolloutHash:       ro,
		UserDataSecret:    hyperv1.SecretReference{Name: userDataName},
		RolloutGeneration: newGen,
	}
	if err := r.commitStatus(ctx, cr, committedGen, func() error {
		cr.Status.Previous = prev
		cr.Status.Current = current
		cr.Status.RolloutHashVersion = rolloutHashFormulaVersion
		apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type: hyperv1.IgnitionPayloadIgnitionReachedCondition, Status: metav1.ConditionFalse,
			Reason: "NewGeneration", Message: "a new payload generation has not yet been reached by a node",
		})
		setPayloadGenerated(cr, observedGen)
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.tokenRefreshInterval()}, nil
}

// refresh is the Policy A path: the rollout hash is unchanged. It ensures the bytes behind the current
// generation match the payload identity (re-rendering in place on a management-side or
// credential-content change), keeps the current (and draining previous) userData tokens fresh, and
// updates only configHash — never advancing rolloutGeneration.
//
// ensurePayload compares against the store's recorded hash, not status.configHash, so it also repairs
// a store entry that diverged from status (e.g. a late Put from an overlapping leader); when the store
// already matches it costs one store read and no render.
func (r *Reconciler) refresh(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef, committedGen, observedGen int64, identity string, gen genInputs) (ctrl.Result, error) {
	if _, err := r.Generator.ensurePayload(ctx, owner, committedGen, identity, gen); err != nil {
		return ctrl.Result{}, err
	}
	// Re-issue tokens / keep userData Secrets fresh for the current (and draining previous)
	// generations, so a future CAPI instance still boots with a valid token.
	if _, err := r.ensureUserDataSecret(ctx, cr, owner, cr.Status.Current.RolloutGeneration); err != nil {
		return ctrl.Result{}, err
	}
	if cr.Status.Previous.RolloutGeneration > 0 {
		if _, err := r.ensureUserDataSecret(ctx, cr, owner, cr.Status.Previous.RolloutGeneration); err != nil {
			return ctrl.Result{}, err
		}
	}
	// Update only configHash (rolloutGeneration is unchanged); the field-scoped optimistic patch
	// preserves a concurrent serving-tier IgnitionReached write.
	if err := r.commitStatus(ctx, cr, committedGen, func() error {
		cr.Status.Current.ConfigHash = identity
		setPayloadGenerated(cr, observedGen)
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.tokenRefreshInterval()}, nil
}

// reconcileInputs are the credential/global-config bytes the CR names, read from its namespace.
type reconcileInputs struct {
	pullSecret          []byte
	trustBundle         []byte
	rolloutGlobalConfig []byte
}

// readInputs reads the pull-secret, trust-bundle, and rollout-global-config the CR references. A
// read failure is transient (the referenced object may not exist yet) and bubbles up as a requeue.
func (r *Reconciler) readInputs(ctx context.Context, cr *hyperv1.IgnitionPayload) (reconcileInputs, error) {
	pullSecret, err := r.readSecretKey(ctx, cr.Namespace, cr.Spec.PullSecret.Name, corev1.DockerConfigJsonKey)
	if err != nil {
		return reconcileInputs{}, err
	}
	trustBundle, err := r.readConfigMapKey(ctx, cr.Namespace, cr.Spec.AdditionalTrustBundle.Name, trustBundleKey)
	if err != nil {
		return reconcileInputs{}, err
	}
	rolloutGlobalConfig, err := r.readConfigMapKey(ctx, cr.Namespace, cr.Spec.RolloutGlobalConfig.Name, configDataKey)
	if err != nil {
		return reconcileInputs{}, err
	}
	return reconcileInputs{pullSecret: pullSecret, trustBundle: trustBundle, rolloutGlobalConfig: rolloutGlobalConfig}, nil
}

// retirePrevious releases the previous generation once the consumer signals it has drained. It is
// guarded by previous.rolloutGeneration <= retiredRolloutGeneration so an earlier generation's drain
// signal does not clear a still-draining previous (A->B->C). Like rollout it is commit-first: it only
// clears status.previous; the next reconcile's sweepOrphans frees the retired payload and userData
// Secret, so status never names a generation that has already been deleted.
func (r *Reconciler) retirePrevious(ctx context.Context, cr *hyperv1.IgnitionPayload, committedGen int64) error {
	if cr.Status.Previous.RolloutGeneration == 0 || cr.Spec.RetiredRolloutGeneration == 0 ||
		cr.Status.Previous.RolloutGeneration > cr.Spec.RetiredRolloutGeneration {
		return nil
	}
	retiredGen := cr.Status.Previous.RolloutGeneration
	return r.commitStatus(ctx, cr, committedGen, func() error {
		// Clear only if previous is still the generation being retired (an intervening rollout may
		// have moved a newer, still-draining generation into previous).
		if cr.Status.Previous.RolloutGeneration == retiredGen {
			cr.Status.Previous = hyperv1.PayloadReference{}
		}
		return nil
	})
}

func setPayloadGenerated(cr *hyperv1.IgnitionPayload, observedGen int64) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type: hyperv1.IgnitionPayloadPayloadGeneratedCondition, Status: metav1.ConditionTrue, Reason: "AsExpected",
		Message: "payload generated for the current config", ObservedGeneration: observedGen,
	})
}

// freeAllGenerations deletes every store entry owned by owner.
func (r *Reconciler) freeAllGenerations(ctx context.Context, owner payloadstore.OwnerRef) error {
	gens, err := r.Store.ListByOwner(ctx, owner)
	if err != nil {
		return err
	}
	for _, g := range gens {
		if err := r.Store.Delete(ctx, owner, g); err != nil {
			return err
		}
	}
	return nil
}

// sweepOrphans is the reclaim step, run once at the start of every reconcile against the
// authoritative read: it deletes every store entry for owner and every CR-controlled userData Secret
// whose generation committed status no longer needs (see reclaimable). It is the only place
// generations are freed outside teardown, so eviction (rollout), retirement, and abandoned generations
// are all reclaimed the same way, and always by a reconcile whose committed status has already
// stopped naming them. It is level-triggered, single-active (the manager is leader-elected), and
// scoped to the one CR being reconciled.
func (r *Reconciler) sweepOrphans(ctx context.Context, owner payloadstore.OwnerRef, cr *hyperv1.IgnitionPayload) error {
	if err := r.sweepOrphanedUserDataSecrets(ctx, cr); err != nil {
		return err
	}
	gens, err := r.Store.ListByOwner(ctx, owner)
	if err != nil {
		return err
	}
	for _, g := range gens {
		if reclaimable(cr, g) {
			if err := r.Store.Delete(ctx, owner, g); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) tokenTTL() time.Duration {
	if r.TokenTTL > 0 {
		return r.TokenTTL
	}
	return defaultTokenTTL
}

// tokenRefreshInterval is how long the controller waits before re-reconciling to re-issue tokens; it
// is half the TTL so a token is always refreshed well before it expires.
func (r *Reconciler) tokenRefreshInterval() time.Duration {
	return r.tokenTTL() / 2
}

func (r *Reconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// readSecretKey returns the bytes under key in the named Secret, or nil if name is empty.
func (r *Reconciler) readSecretKey(ctx context.Context, ns, name, key string) ([]byte, error) {
	if name == "" {
		return nil, nil
	}
	s := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, s); err != nil {
		return nil, err
	}
	return s.Data[key], nil
}

// readConfigMapKey returns the bytes under key in the named ConfigMap, or nil if name is empty.
func (r *Reconciler) readConfigMapKey(ctx context.Context, ns, name, key string) ([]byte, error) {
	if name == "" {
		return nil, nil
	}
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, cm); err != nil {
		return nil, err
	}
	return []byte(cm.Data[key]), nil
}

// getHostedControlPlane returns the single HostedControlPlane in the controller's namespace. Each
// PayloadController instance is scoped to one HCP namespace, so exactly one is expected; its absence
// is transient (requeue). Reading the in-namespace HCP keeps the generator consumer-agnostic — it is
// a namespace-level fact, not a back-reference to any one consumer's NodePool.
func (r *Reconciler) getHostedControlPlane(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
	list := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, list, client.InNamespace(r.Namespace)); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("no HostedControlPlane found in namespace %q", r.Namespace)
	}
	return &list.Items[0], nil
}

// cloudConfigHash returns the hash of the platform cloud-provider config ConfigMap, mirroring
// ConfigGenerator.GetCloudConfigHash: a value for Azure/OpenStack, "" for platforms without one
// (e.g. AWS/None) or when the ConfigMap is not yet present.
func (r *Reconciler) cloudConfigHash(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	var cm *corev1.ConfigMap
	switch hcp.Spec.Platform.Type {
	case hyperv1.AzurePlatform:
		cm = manifests.AzureProviderConfig(r.Namespace)
	case hyperv1.OpenStackPlatform:
		cm = manifests.OpenStackProviderConfig(r.Namespace)
	default:
		return "", nil
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cm), cm); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get cloud config ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
	}
	return supportutil.HashConfigMapData(cm.Data), nil
}

func joinManifests(r ResolvedConfig) string {
	parts := make([]string, 0, 2)
	if r.RolloutManifests != "" {
		parts = append(parts, r.RolloutManifests)
	}
	if r.ManagementManifests != "" {
		parts = append(parts, r.ManagementManifests)
	}
	return strings.Join(parts, "\n---\n")
}

// ensureUserDataSecret creates-or-updates the CR-owned pointer-ignition userData Secret named
// <ignitionpayload-name>-<rolloutGeneration>. It mints a fresh token only when the Secret is missing
// or its token is past half its TTL, so re-issue never churns on every reconcile and never advances
// serving identity (the name, path, and header stay the same). It returns the Secret name.
func (r *Reconciler) ensureUserDataSecret(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef, rolloutGeneration int64) (string, error) {
	name := userDataSecretName(cr.Name, rolloutGeneration)
	secret := &corev1.Secret{}
	secret.Namespace = cr.Namespace
	secret.Name = name

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(cr, secret, r.Scheme()); err != nil {
			return err
		}
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		if r.tokenFresh(secret) && len(secret.Data[userDataValueKey]) > 0 {
			return nil
		}
		token, err := r.Minter.Mint(owner, r.tokenTTL())
		if err != nil {
			return err
		}
		value, err := marshalPointerIgnition(pointerIgnitionConfig(r.IgnitionCACert, token, r.IgnitionServerEndpoint, cr.Name, rolloutGeneration))
		if err != nil {
			return err
		}
		secret.Data = map[string][]byte{
			userDataDisableTemplatingKey: []byte(base64.StdEncoding.EncodeToString([]byte("true"))),
			userDataValueKey:             value,
		}
		secret.Annotations[tokenIssuedAtAnnotation] = r.clock().Format(time.RFC3339)
		return nil
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// tokenFresh reports whether the userData Secret's token was minted recently enough to not need
// re-issue yet (within the first half of its TTL).
func (r *Reconciler) tokenFresh(secret *corev1.Secret) bool {
	ts := secret.Annotations[tokenIssuedAtAnnotation]
	if ts == "" {
		return false
	}
	issued, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return r.clock().Sub(issued) < r.tokenRefreshInterval()
}

// reclaimable reports whether generation g may be freed given committed status. Current and previous
// are advertised and kept. A generation above current is kept too: generations only increase and a
// rollout always prepares current+1, so anything above current is an uncommitted prepare that the next
// rollout reuses (skipping a re-render when its inputs match) or overwrites — at most one such
// generation can exist, so keeping it is bounded. Everything else is below current and no longer
// named by status: evicted, retired, or abandoned.
func reclaimable(cr *hyperv1.IgnitionPayload, g int64) bool {
	current, previous := cr.Status.Current.RolloutGeneration, cr.Status.Previous.RolloutGeneration
	return g < current && g != previous
}

// sweepOrphanedUserDataSecrets deletes the userData Secrets this CR controls whose generation is
// reclaimable. Ownership is matched by the controller reference (the CR's UID) rather than by name,
// since <name>-<generation> names of different CRs can overlap; the generation is parsed from the
// name, and a CR-controlled Secret that is not named <name>-<generation> is not a userData Secret this
// controller authored and is left alone. The List is served from the namespace cache; that is safe
// because the keep-set comes from committed status, so a lagging cache can only defer a deletion to
// the next reconcile, never delete a referenced Secret.
func (r *Reconciler) sweepOrphanedUserDataSecrets(ctx context.Context, cr *hyperv1.IgnitionPayload) error {
	secrets := &corev1.SecretList{}
	if err := r.List(ctx, secrets, client.InNamespace(cr.Namespace)); err != nil {
		return err
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if !metav1.IsControlledBy(secret, cr) {
			continue
		}
		g, ok := userDataSecretGeneration(cr.Name, secret.Name)
		if !ok || !reclaimable(cr, g) {
			continue
		}
		if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// userDataSecretGeneration is the inverse of userDataSecretName: it parses the rollout generation from
// a <payloadName>-<generation> Secret name.
func userDataSecretGeneration(payloadName, secretName string) (int64, bool) {
	suffix, ok := strings.CutPrefix(secretName, payloadName+"-")
	if !ok {
		return 0, false
	}
	g, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || g <= 0 {
		return 0, false
	}
	return g, true
}

func userDataSecretName(payloadName string, rolloutGeneration int64) string {
	return payloadName + "-" + strconv.FormatInt(rolloutGeneration, 10)
}
