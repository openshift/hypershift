package ignitionpayloadserver

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1ac "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fieldManager scopes the serving tier's status writes so Server-Side Apply only ever owns the
// IgnitionReached condition, never the generator-owned current/previous/PayloadGenerated fields.
const fieldManager = "ignition-payload-server"

// SetIgnitionReached sets the IgnitionReached condition to True on the owning IgnitionPayload CR,
// but ONLY when the generation the node presented still matches status.current.rolloutGeneration
// (so a serve on the draining previous generation, or a serve after the generation advanced, does
// not flip it) and it is not already True. It writes a field-scoped Server-Side Apply under its own
// field manager, so it never clobbers the generator-owned status.current/previous/PayloadGenerated;
// the pre-read gate re-checks the live generation each call, so a serve racing a rollout is dropped
// rather than marking the new generation reached.
func SetIgnitionReached(ctx context.Context, c client.Client, owner payloadstore.OwnerRef, rolloutGeneration int64) error {
	cr := &hyperv1.IgnitionPayload{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: owner.Namespace, Name: owner.Name}, cr); err != nil {
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
		WithStatus(hypershiftv1ac.IgnitionPayloadStatus().
			WithConditions(metav1ac.Condition().
				WithType(hyperv1.IgnitionPayloadIgnitionReachedCondition).
				WithStatus(metav1.ConditionTrue).
				WithReason("Served").
				WithMessage("ignition payload served to a node").
				WithLastTransitionTime(metav1.Now()).
				WithObservedGeneration(cr.Generation)))

	return c.Status().Apply(ctx, applyCfg, client.FieldOwner(fieldManager), client.ForceOwnership)
}
