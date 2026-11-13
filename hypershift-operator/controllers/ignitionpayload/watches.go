package ignitionpayload

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SetupWithManager registers the reconciler: it owns IgnitionPayload and re-reconciles a CR when
// any ConfigMap or Secret the CR references by name changes.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.IgnitionPayload{}).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.enqueueIgnitionPayloadsForConfigMap)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.enqueueIgnitionPayloadsForSecret)).
		Complete(r)
}

// enqueueIgnitionPayloadsForConfigMap enqueues every IgnitionPayload in the ConfigMap's namespace
// that references it by name (rolloutConfigMaps, mgmtConfigMaps, rolloutGlobalConfig, or
// additionalTrustBundle).
func (r *Reconciler) enqueueIgnitionPayloadsForConfigMap(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueueMatching(ctx, obj.GetNamespace(), func(cr *hyperv1.IgnitionPayload) bool {
		return crReferencesConfigMap(cr, obj.GetName())
	})
}

// enqueueIgnitionPayloadsForSecret enqueues every IgnitionPayload in the Secret's namespace that
// references it as its pull secret.
func (r *Reconciler) enqueueIgnitionPayloadsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.enqueueMatching(ctx, obj.GetNamespace(), func(cr *hyperv1.IgnitionPayload) bool {
		return cr.Spec.PullSecret.Name == obj.GetName()
	})
}

func (r *Reconciler) enqueueMatching(ctx context.Context, namespace string, match func(*hyperv1.IgnitionPayload) bool) []reconcile.Request {
	list := &hyperv1.IgnitionPayloadList{}
	if err := r.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		cr := &list.Items[i]
		if match(cr) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cr)})
		}
	}
	return reqs
}

func crReferencesConfigMap(cr *hyperv1.IgnitionPayload, name string) bool {
	if cr.Spec.AdditionalTrustBundle.Name == name || cr.Spec.RolloutGlobalConfig.Name == name {
		return true
	}
	for _, ref := range cr.Spec.RolloutConfigMaps {
		if ref.Name == name {
			return true
		}
	}
	for _, ref := range cr.Spec.ManagementConfigMaps {
		if ref.Name == name {
			return true
		}
	}
	return false
}
