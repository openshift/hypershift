package oauth

import (
	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
)

func ReconcileRoute(route *routev1.Route, ownerRef config.OwnerRef, private bool) error {
	ownerRef.ApplyTo(route)
	if private {
		if route.Labels == nil {
			route.Labels = map[string]string{}
		}
		route.Labels[ingress.HypershiftRouteLabel] = route.GetNamespace()
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
