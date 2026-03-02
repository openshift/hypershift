package primaryudn

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	hyperapi "github.com/openshift/hypershift/support/api"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "primary-udn-guest-fixups"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	if opts.PlatformType != hyperv1.KubevirtPlatform {
		return nil
	}

	logger := ctrl.LoggerFrom(ctx)

	// The Primary UDN annotation on the HCP is immutable, so checking once at setup
	// is sufficient. Use a direct API client to read the HCP from the management
	// cluster — it's namespace-scoped, so the HCCO SA has access.
	directClient, err := client.New(opts.Config, client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create direct client: %w", err)
	}
	hcp := &hyperv1.HostedControlPlane{}
	if err := directClient.Get(ctx, types.NamespacedName{Namespace: opts.Namespace, Name: opts.HCPName}, hcp); err != nil {
		return fmt.Errorf("failed to get HostedControlPlane %s/%s: %w", opts.Namespace, opts.HCPName, err)
	}
	if hcp.Annotations == nil || hcp.Annotations["hypershift.openshift.io/primary-udn"] != "true" {
		logger.Info("Skipping controller: HCP is not Primary UDN", "controller", ControllerName)
		return nil
	}

	r := &reconciler{
		guestClient: opts.Manager.GetClient(),
		mgmtClient:  opts.CPCluster.GetClient(),
		namespace:   opts.Namespace,
		hcpName:     opts.HCPName,
	}

	enqueueHCP := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: r.namespace, Name: r.hcpName}}}
	})

	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Re-reconcile when OAuth pods or routes change in the management cluster.
	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &corev1.Pod{}, enqueueHCP, isOAuthPodInNamespace(opts.Namespace))); err != nil {
		return fmt.Errorf("failed to watch OAuth pods: %w", err)
	}
	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &routev1.Route{}, enqueueHCP, isOAuthRouteInNamespace(opts.Namespace))); err != nil {
		return fmt.Errorf("failed to watch OAuth routes: %w", err)
	}

	logger.Info("Setup", "controller", ControllerName)
	return nil
}
