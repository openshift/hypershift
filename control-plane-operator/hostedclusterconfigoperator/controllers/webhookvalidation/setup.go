package webhookvalidation

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "webhook-validation"

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

	return nil
}

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
