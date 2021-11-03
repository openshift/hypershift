package resources

import (
	"context"
	"fmt"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/registry"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
	"github.com/openshift/hypershift/support/upsert"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "resources"

type reconciler struct {
	client crclient.Client
	upsert.CreateOrUpdateProvider
	platformType hyperv1.PlatformType
}

// eventHandler is the handler used throughout. As this controller reconciles all kind of different resources
// it uses an empty request but always reconciles everything.
func eventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(crclient.Object) []reconcile.Request {
			return []reconcile.Request{{}}
		})
}

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	if err := imageregistryv1.AddToScheme(opts.Manager().GetScheme()); err != nil {
		return fmt.Errorf("failed to add to scheme: %w", err)
	}
	c, err := controller.New(ControllerName, opts.Manager(), controller.Options{Reconciler: &reconciler{
		client:                 opts.Manager().GetClient(),
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
		platformType:           opts.PlatformType,
	}})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(&source.Kind{Type: &imageregistryv1.Config{}}, eventHandler()); err != nil {
		return fmt.Errorf("failed to watch imageregistryv1.Config: %w", err)
	}

	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, r.reconcile(ctx)
}

func (r *reconciler) reconcile(ctx context.Context) error {
	registryConfig := manifests.Registry()
	if _, err := r.CreateOrUpdate(ctx, r.client, registryConfig, func() error {
		registry.ReconcileRegistryConfig(registryConfig, r.platformType == hyperv1.NonePlatform)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile imageregistry config: %w", err)
	}

	return nil
}
