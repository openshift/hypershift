package ignitionconsumer

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	pkgmanifests "github.com/openshift/hypershift/pkg/manifests"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// EnqueueNodePool maps an IgnitionPayload CR back to its owning NodePool via the
// back-reference annotation (ns/name). CRs without a well-formed annotation (foreign consumers,
// hand-created objects) are ignored.
func EnqueueNodePool(_ context.Context, obj client.Object) []reconcile.Request {
	val, ok := obj.GetAnnotations()[nodePoolAnnotation]
	if !ok {
		return nil
	}
	parts := strings.SplitN(val, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: parts[0], Name: parts[1]}}}
}

// AdvanceRetiredRolloutGeneration moves spec.retiredRolloutGeneration forward to gen once that
// generation has drained. It is level-triggered and monotonic. The guard is evaluated against a
// freshly-read object and the patch carries an optimistic lock, so the contract holds even when the
// caller passes a stale cr: a gen at or below the server value never regresses it, and a concurrent
// writer causes a conflict the caller can retry. The caller's cr is synced to the resulting value.
func AdvanceRetiredRolloutGeneration(ctx context.Context, c client.Client, cr *hyperv1.IgnitionPayload, gen int64) error {
	fresh := &hyperv1.IgnitionPayload{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(cr), fresh); err != nil {
		return fmt.Errorf("failed to read IgnitionPayload before advancing retiredRolloutGeneration: %w", err)
	}
	if gen <= fresh.Spec.RetiredRolloutGeneration {
		cr.Spec.RetiredRolloutGeneration = fresh.Spec.RetiredRolloutGeneration
		return nil
	}
	patch := client.MergeFromWithOptions(fresh.DeepCopy(), client.MergeFromWithOptimisticLock{})
	fresh.Spec.RetiredRolloutGeneration = gen
	if err := c.Patch(ctx, fresh, patch); err != nil {
		return fmt.Errorf("failed to advance retiredRolloutGeneration to %d: %w", gen, err)
	}
	cr.Spec.RetiredRolloutGeneration = gen
	return nil
}

// Inputs carries the resolved inputs the Phase-5 live reconcile supplies to the
// consumer: the gathered config ConfigMaps, the release-derived values, and the endpoint-identity
// rollout input. Release-image resolution, HAProxy generation, and the endpoint derivation
// (endpointConfigString) are the live-reconcile wiring deferred to Phase 5; here they are inputs so
// the orchestration is unit-testable. Node-facing userData authoring + token minting are no longer
// the consumer's job — the generator owns them — so caCert/endpoint/proxy are gone.
type Inputs struct {
	ReleaseVersion string
	OSStream       string
	// AdditionalRolloutInputs is the API-server endpoint identity (endpointConfigString) folded into
	// the rollout hash so an endpoint change rolls running nodes.
	AdditionalRolloutInputs string
	UserConfigs             []corev1.ConfigMap
	CoreConfigs             []corev1.ConfigMap
	NTOConfigs              []corev1.ConfigMap
	HAProxyRaw              string
	MachineSetName          string
}

// Reconcile composes the Phase-4 building blocks for one NodePool: author the
// rolloutGlobalConfig, ensure the CR exists (so its UID can own the projected ConfigMaps), project +
// classify the configs, write the full CR spec, and — once the generator has published
// status.current (with the userData Secret it authored) — write the legacy in-place compat Secret
// for InPlace pools. It returns status.current.userDataSecretName, the generator-authored Secret the
// CAPI re-point will consume, or "" when no payload has been generated yet. The consumer never mints
// userdata or handles the token; it only references the generator's Secret by name. NOT wired into
// the live reconcile in this phase.
func Reconcile(ctx context.Context, c client.Client, store payloadstore.PayloadStore,
	nodePool *hyperv1.NodePool, hc *hyperv1.HostedCluster, in Inputs) (string, error) {
	hcpNamespace := pkgmanifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

	rolloutGlobalConfig, err := rolloutGlobalConfigData(hc, in.ReleaseVersion)
	if err != nil {
		return "", err
	}

	// Classify the configs into deterministic reference names first, so the full CR spec is authored
	// in a single write (no intermediate empty-ref spec that would churn the CR and thrash rollouts).
	rolloutRefs, mgmtRefs, globalName := classifyConfigs(nodePool.GetName(), in.UserConfigs, in.CoreConfigs, in.NTOConfigs)
	fullSpec := ignitionPayloadSpec(nodePool, hc, rolloutRefs, mgmtRefs, globalName, in.OSStream, in.AdditionalRolloutInputs)
	cr, err := reconcileIgnitionPayloadCR(ctx, c, hcpNamespace, nodePool, fullSpec)
	if err != nil {
		return "", err
	}

	// Materialize the CR-owned ConfigMaps the spec references (owner refs need the CR's UID, now
	// available). A brief window where the spec references not-yet-created ConfigMaps is benign: the
	// generator treats a missing ConfigMap as a transient requeue.
	if err := materializeConfigs(ctx, c, hcpNamespace, cr, in.UserConfigs, in.HAProxyRaw, rolloutGlobalConfig); err != nil {
		return "", err
	}

	// Until the generator publishes status.current (with the userData Secret it authored) there is
	// nothing for the consumer to point machines at.
	if cr.Status.Current.UserDataSecret.Name == "" {
		return "", nil
	}

	// The generator authors the node-facing userData Secret; the consumer only references it by name.
	// For InPlace pools the consumer additionally writes the byte-compatible legacy Secret the HCCO
	// reads directly (no HTTP fetch). That Secret pairs status.current's payload with the consumer's
	// ReleaseVersion, so it is written only once the generator has processed the spec just authored:
	// until then status.current may still be the previous release's payload, and the HCCO would stamp
	// the new release version onto Machines that completed the old payload. The previous userData
	// Secret is still returned meanwhile, so machines stay on it until the new generation commits.
	if payloadGeneratedForSpec(cr) {
		if err := reconcileLegacyInPlaceSecret(ctx, c, store, in.MachineSetName,
			nodePool.Spec.Management.UpgradeType, in.ReleaseVersion, cr.Status.Current, cr); err != nil {
			return "", err
		}
	}

	return cr.Status.Current.UserDataSecret.Name, nil
}

// payloadGeneratedForSpec reports whether the generator has generated status.current from the CR's
// latest spec: PayloadGenerated is True and its observedGeneration matches metadata.generation.
func payloadGeneratedForSpec(cr *hyperv1.IgnitionPayload) bool {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, hyperv1.IgnitionPayloadPayloadGeneratedCondition)
	return cond != nil && cond.Status == metav1.ConditionTrue && cond.ObservedGeneration == cr.Generation
}

// Finalize removes the consumer finalizer from the NodePool's IgnitionPayload
// CR so it (and its owned ConfigMaps + the generator-authored userData Secrets, via owner-reference
// garbage collection, once the generator's store-cleanup finalizer also clears) can be reclaimed.
// Other controllers' finalizers are left intact. An absent CR is a no-op.
func Finalize(ctx context.Context, c client.Client, hcpNamespace, nodePoolName string) error {
	cr := &hyperv1.IgnitionPayload{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: hcpNamespace, Name: nodePoolName}, cr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !sets.New(cr.Finalizers...).Has(consumerFinalizer) {
		return nil
	}
	// A merge patch replaces the whole finalizers list, so it carries an optimistic lock: without it,
	// a finalizer added after the read (e.g. the generator's store-cleanup finalizer) would be dropped
	// and the CR could be deleted before the store is cleaned up. A conflict is returned and the next
	// reconcile re-reads.
	patch := client.MergeFromWithOptions(cr.DeepCopy(), client.MergeFromWithOptimisticLock{})
	cr.Finalizers = sets.List(sets.New(cr.Finalizers...).Delete(consumerFinalizer))
	if err := c.Patch(ctx, cr, patch); err != nil {
		return fmt.Errorf("failed to remove consumer finalizer from IgnitionPayload %s/%s: %w", hcpNamespace, nodePoolName, err)
	}
	return nil
}
