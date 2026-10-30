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

	// defaultTokenTTL is how long a minted bearer token is valid. The re-issue timer re-mints well
	// before expiry so a future CAPI instance created hours after the rollout still boots with a
	// valid token.
	defaultTokenTTL = 2 * time.Hour
)

// Reconciler generates ignition payloads for IgnitionPayload CRs: it reads the config the CR names,
// validates it, computes the payload-identity and rollout hashes, renders+stores payloads keyed by
// rollout identity (owner + rolloutGeneration), authors the pointer-ignition userData Secret, and
// advances status to drive rollouts.
type Reconciler struct {
	client.Client
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
	Client                 client.Client
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

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cr := &hyperv1.IgnitionPayload{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
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
				Type:    hyperv1.IgnitionPayloadPayloadGeneratedCondition,
				Status:  metav1.ConditionFalse,
				Reason:  "InvalidConfig",
				Message: err.Error(),
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

	if err := r.retirePrevious(ctx, cr, owner); err != nil {
		return ctrl.Result{}, err
	}

	gen := genInputs{
		releaseImage:        cr.Spec.ReleaseImage,
		customConfig:        joinManifests(resolved),
		pullSecretHash:      supportutil.HashSimple(inputs.pullSecret),
		trustBundleHash:     supportutil.HashSimple(inputs.trustBundle),
		hcConfigurationHash: hcConfigHash,
		osImageStream:       cr.Spec.OSImageStream.Name,
		cloudConfigHash:     cloudConfigHash,
	}

	// Rebaseline: if the stored formula version is older than the binary's, rewrite the stored
	// rollout hash to the new-formula value WITHOUT advancing rolloutGeneration. Serving identity
	// (rolloutGeneration, the store entry, the userData Secret, the node header) is keyed on the
	// generation, not the hash, so the rebaseline never orphans a live payload.
	if cr.Status.Current.RolloutGeneration > 0 && cr.Status.RolloutHashVersion < rolloutHashFormulaVersion {
		err := statuspatching.PatchStatus(ctx, r.Client, cr, func() error {
			cr.Status.Current.RolloutHash = ro
			cr.Status.RolloutHashVersion = rolloutHashFormulaVersion
			return nil
		})
		return ctrl.Result{RequeueAfter: r.tokenRefreshInterval()}, err
	}

	if cr.Status.Current.RolloutGeneration == 0 || ro != cr.Status.Current.RolloutHash {
		return r.rollout(ctx, cr, owner, identity, ro, gen)
	}
	return r.refresh(ctx, cr, owner, identity, gen)
}

// rollout advances rolloutGeneration: it evicts the old previous payload (delete-on-evict, so at
// most two payloads are ever live even when rollouts overtake each other), moves current->previous,
// generates+stores under the new generation, authors its userData Secret, and resets IgnitionReached.
func (r *Reconciler) rollout(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef, identity, ro string, gen genInputs) (ctrl.Result, error) {
	if cr.Status.Previous.RolloutGeneration > 0 {
		if err := r.freeGeneration(ctx, cr, owner, cr.Status.Previous.RolloutGeneration); err != nil {
			return ctrl.Result{}, err
		}
	}
	newGen := cr.Status.Current.RolloutGeneration + 1
	// prev is the generation this rollout supersedes; captured by value before the status patch
	// re-fetches cr, so it survives an optimistic-lock retry.
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
	if err := statuspatching.PatchStatus(ctx, r.Client, cr, func() error {
		cr.Status.Previous = prev
		cr.Status.Current = current
		cr.Status.RolloutHashVersion = rolloutHashFormulaVersion
		apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type: hyperv1.IgnitionPayloadIgnitionReachedCondition, Status: metav1.ConditionFalse,
			Reason: "NewGeneration", Message: "a new payload generation has not yet been reached by a node",
		})
		setPayloadGenerated(cr)
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	// Sweep after the status patch so the keep-set reflects the advanced current/previous.
	if err := r.sweepOrphans(ctx, owner, cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.tokenRefreshInterval()}, nil
}

// refresh is the Policy A path: the rollout hash is unchanged. If the payload-identity changed
// (management-side or credential-content change) it refreshes the bytes behind the current
// generation in place, keeps the current (and draining previous) userData tokens fresh, and updates
// only configHash — never advancing rolloutGeneration.
func (r *Reconciler) refresh(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef, identity string, gen genInputs) (ctrl.Result, error) {
	if identity != cr.Status.Current.ConfigHash {
		if _, err := r.Generator.ensurePayload(ctx, owner, cr.Status.Current.RolloutGeneration, identity, gen); err != nil {
			return ctrl.Result{}, err
		}
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
	if err := statuspatching.PatchStatus(ctx, r.Client, cr, func() error {
		cr.Status.Current.ConfigHash = identity
		setPayloadGenerated(cr)
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.sweepOrphans(ctx, owner, cr); err != nil {
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

// retirePrevious frees the previous payload and its userData Secret once the consumer signals its
// generation has drained. It is guarded by previous.rolloutGeneration <= retiredRolloutGeneration so
// an earlier generation's drain signal does not clear a still-draining previous (A->B->C).
func (r *Reconciler) retirePrevious(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef) error {
	if cr.Status.Previous.RolloutGeneration == 0 || cr.Spec.RetiredRolloutGeneration == 0 ||
		cr.Status.Previous.RolloutGeneration > cr.Spec.RetiredRolloutGeneration {
		return nil
	}
	retiredGen := cr.Status.Previous.RolloutGeneration
	if err := r.freeGeneration(ctx, cr, owner, retiredGen); err != nil {
		return err
	}
	return statuspatching.PatchStatus(ctx, r.Client, cr, func() error {
		// Clear only if previous is still the generation we just freed (an intervening rollout may
		// have moved a newer, still-draining generation into previous).
		if cr.Status.Previous.RolloutGeneration == retiredGen {
			cr.Status.Previous = hyperv1.PayloadReference{}
		}
		return nil
	})
}

// freeGeneration deletes a generation's store entry and its userData Secret.
func (r *Reconciler) freeGeneration(ctx context.Context, cr *hyperv1.IgnitionPayload, owner payloadstore.OwnerRef, rolloutGeneration int64) error {
	if err := r.Store.Delete(ctx, owner, rolloutGeneration); err != nil {
		return err
	}
	return r.deleteUserDataSecret(ctx, cr.Namespace, cr.Name, rolloutGeneration)
}

func setPayloadGenerated(cr *hyperv1.IgnitionPayload) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type: hyperv1.IgnitionPayloadPayloadGeneratedCondition, Status: metav1.ConditionTrue, Reason: "AsExpected",
		Message: "payload generated for the current config",
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

// sweepOrphans deletes any store entry for owner not referenced by status.current or
// status.previous. It is level-triggered, single-active (the manager is leader-elected), and scoped
// to the one CR being reconciled, so it cannot race a concurrent generator. This reclaims entries an
// interrupted generation left unreferenced (crash between Put and the status write).
func (r *Reconciler) sweepOrphans(ctx context.Context, owner payloadstore.OwnerRef, cr *hyperv1.IgnitionPayload) error {
	keep := map[int64]bool{}
	if cr.Status.Current.RolloutGeneration > 0 {
		keep[cr.Status.Current.RolloutGeneration] = true
	}
	if cr.Status.Previous.RolloutGeneration > 0 {
		keep[cr.Status.Previous.RolloutGeneration] = true
	}
	gens, err := r.Store.ListByOwner(ctx, owner)
	if err != nil {
		return err
	}
	for _, g := range gens {
		if !keep[g] {
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

// deleteUserDataSecret removes the userData Secret for a given rollout generation. No-op if absent.
func (r *Reconciler) deleteUserDataSecret(ctx context.Context, ns, payloadName string, rolloutGeneration int64) error {
	secret := &corev1.Secret{}
	secret.Namespace = ns
	secret.Name = userDataSecretName(payloadName, rolloutGeneration)
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func userDataSecretName(payloadName string, rolloutGeneration int64) string {
	return payloadName + "-" + strconv.FormatInt(rolloutGeneration, 10)
}
