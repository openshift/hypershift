package oauth

import (
	"fmt"

	routev1 "github.com/openshift/api/route/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileRoute(route *routev1.Route, ownerRef config.OwnerRef, strategy *hyperv1.ServicePublishingStrategy, defaultIngressDomain string, hcp *hyperv1.HostedControlPlane) error {
	ownerRef.ApplyTo(route)

	// The route host is considered immutable, so set it only once upon creation
	// and ignore updates.
	if route.CreationTimestamp.IsZero() {
		switch {
		// Private router with public LB, use service domain DNS if present and private router
		case util.HasPublicLoadBalancerForPrivateRouter(hcp) && strategy.Route != nil && strategy.Route.Hostname != "":
			route.Spec.Host = strategy.Route.Hostname
			ingress.AddRouteLabel(route)

		// Public cluster with Service domain DNS without public LB on private router: Use service
		// domain Hostname, otherwise the name-based virtual hosting on the router won't work
		case util.IsPublicHCP(hcp) && strategy.Route != nil && strategy.Route.Hostname != "":
			route.Spec.Host = strategy.Route.Hostname

		// Private cluster: Private DNS
		case !util.IsPublicHCP(hcp):
			route.Spec.Host = fmt.Sprintf("oauth.apps.%s.hypershift.local", hcp.Name)
			ingress.AddRouteLabel(route)

		// Public cluster without service domain: Fall through to using the default router
		default:
			route.Spec.Host = util.ShortenRouteHostnameIfNeeded(route.Name, route.Namespace, defaultIngressDomain)
		}
	}

	if strategy.Route != nil && strategy.Route.Hostname != "" {
		if route.Annotations == nil {
			route.Annotations = map[string]string{}
		}
		route.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = strategy.Route.Hostname
	}

	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service",
		Name: manifests.OauthServerService(route.Namespace).Name,
	}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
	return nil
}
