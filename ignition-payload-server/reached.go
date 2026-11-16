package ignitionpayloadserver

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1ac "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fieldManager scopes the serving tier's status writes so Server-Side Apply only ever owns the
// IgnitionReached condition, never the generator-owned current/previous/PayloadGenerated fields.
const fieldManager = "ignition-payload-server"

// SetIgnitionReached sets the IgnitionReached condition to True on the owning IgnitionPayload CR, but
// ONLY when the generation the node presented matches status.current.rolloutGeneration (so a serve on
// the draining previous generation does not flip it) and it is not already True. It writes a
// field-scoped Server-Side Apply under its own field manager, so it never clobbers the generator-owned
// status.current/previous/PayloadGenerated.
//
// The gate object is read from the uncached apiReader so a payload served via the handler's read-through
// is matched against the true current generation, not a lagging cached one. The apply is pinned to that
// read's resourceVersion and retried on conflict: if the CR changed since the read we re-read and
// re-evaluate the gate — skipping when the generation has advanced (a stale-generation serve must not
// mark a generation whose nodes have not booted and reached ignition yet) and re-applying when it was an
// unrelated same-generation refresh (e.g. a configHash update), so a legitimate write is not dropped.
func SetIgnitionReached(ctx context.Context, c client.Client, apiReader client.Reader, owner payloadstore.OwnerRef, rolloutGeneration int64) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cr := &hyperv1.IgnitionPayload{}
		if err := apiReader.Get(ctx, client.ObjectKey{Namespace: owner.Namespace, Name: owner.Name}, cr); err != nil {
			return client.IgnoreNotFound(err)
		}

		// Gate: only the current generation flips the condition.
		if cr.Status.Current.RolloutGeneration != rolloutGeneration {
			return nil
		}
		if apimeta.IsStatusConditionTrue(cr.Status.Conditions, hyperv1.IgnitionPayloadIgnitionReachedCondition) {
			return nil
		}

		applyCfg := hypershiftv1ac.IgnitionPayload(owner.Name, owner.Namespace).
			WithResourceVersion(cr.ResourceVersion).
			WithStatus(hypershiftv1ac.IgnitionPayloadStatus().
				WithConditions(metav1ac.Condition().
					WithType(hyperv1.IgnitionPayloadIgnitionReachedCondition).
					WithStatus(metav1.ConditionTrue).
					WithReason("Served").
					WithMessage("ignition payload served to a node").
					WithLastTransitionTime(metav1.Now()).
					WithObservedGeneration(cr.Generation)))

		return c.Status().Apply(ctx, applyCfg, client.FieldOwner(fieldManager), client.ForceOwnership)
	})
}
