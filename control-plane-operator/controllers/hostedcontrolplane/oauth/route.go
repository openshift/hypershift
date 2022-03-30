package oauth

import (
	routev1 "github.com/openshift/api/route/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileRoute(route *routev1.Route, ownerRef config.OwnerRef, strategy *hyperv1.ServicePublishingStrategy, defaultIngressDomain string) error {
	ownerRef.ApplyTo(route)
	if strategy.Route != nil && strategy.Route.Hostname != "" {
		if route.Annotations == nil {
			route.Annotations = map[string]string{}
		}
		route.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = strategy.Route.Hostname
		route.Spec.Host = strategy.Route.Hostname
	} else {
		route.Spec.Host = util.ShortenRouteHostnameIfNeeded(route.Name, route.Namespace, defaultIngressDomain)
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
