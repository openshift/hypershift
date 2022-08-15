package oauth

import (
	"fmt"

	routev1 "github.com/openshift/api/route/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
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
		case strategy.Route != nil && strategy.Route.Hostname != "":
			route.Spec.Host = strategy.Route.Hostname
			ingress.AddRouteLabel(route)
		case !util.IsPublicHCP(hcp):
			route.Spec.Host = fmt.Sprintf("oauth.apps.%s.hypershift.local", hcp.Name)
			ingress.AddRouteLabel(route)
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
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
	}
	return nil
}
