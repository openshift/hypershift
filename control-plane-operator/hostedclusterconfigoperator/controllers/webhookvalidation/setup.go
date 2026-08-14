package webhookvalidation

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "webhook-validation"

// serviceEventKey is a sentinel name used when a CP Service event triggers
// re-evaluation of all webhook configs of a given type. Reconcile detects this
// and lists all configs instead of fetching a single one.
const serviceEventKey = "*"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &reconciler{
		client:       opts.Manager.GetClient(),
		cpClient:     opts.CPCluster.GetClient(),
		hcpNamespace: opts.Namespace,
	}

	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), &admissionregistrationv1.ValidatingWebhookConfiguration{}, typedHandler(validatingType))); err != nil {
		return fmt.Errorf("failed to watch ValidatingWebhookConfigurations: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), &admissionregistrationv1.MutatingWebhookConfiguration{}, typedHandler(mutatingType))); err != nil {
		return fmt.Errorf("failed to watch MutatingWebhookConfigurations: %w", err)
	}

	// Watch CP Services so that when the disallowed URL list changes (Service created/deleted/relabeled),
	// all existing webhook configs are re-evaluated immediately rather than waiting for cache resync.
	// Service events enqueue sentinel requests; Reconcile lists the configs so list errors are retried.
	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &corev1.Service{}, serviceEventHandler())); err != nil {
		return fmt.Errorf("failed to watch control plane Services: %w", err)
	}

	return nil
}

// Webhook configs are cluster-scoped so Namespace is normally empty; we repurpose it
// to carry the webhook kind ("validating"/"mutating") so Reconcile targets only the type that fired.
func typedHandler(wt webhookType) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Namespace: wt.name,
				Name:      obj.GetName(),
			},
		}}
	})
}

// serviceEventHandler enqueues one sentinel request per webhook type when a CP
// Service changes. The listing of webhook configs happens inside Reconcile so
// that list errors are surfaced and retried by the controller work queue.
func serviceEventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []reconcile.Request {
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: validatingType.name, Name: serviceEventKey}},
			{NamespacedName: types.NamespacedName{Namespace: mutatingType.name, Name: serviceEventKey}},
		}
	})
}
