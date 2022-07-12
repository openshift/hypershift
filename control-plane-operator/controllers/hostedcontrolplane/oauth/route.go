package oauth

import (
	routev1 "github.com/openshift/api/route/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	// hyperShiftOAuthRouteLabel is a label added to OAuth routes so that they
	// can be selected via label selector in a dedicated router shard.
	hyperShiftOAuthRouteLabel = "hypershift.openshift.io/oauth"
)

func ReconcileRoute(route *routev1.Route, ownerRef config.OwnerRef, strategy *hyperv1.ServicePublishingStrategy, defaultIngressDomain string) error {
	ownerRef.ApplyTo(route)

	// The route host is considered immutable, so set it only once upon creation
	// and ignore updates.
	if route.CreationTimestamp.IsZero() {
		switch {
		case strategy.Route != nil && strategy.Route.Hostname != "":
			route.Spec.Host = strategy.Route.Hostname
		default:
			route.Spec.Host = util.ShortenRouteHostnameIfNeeded(route.Name, route.Namespace, defaultIngressDomain)
		}
	}

	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Labels[hyperShiftOAuthRouteLabel] = "true"

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
