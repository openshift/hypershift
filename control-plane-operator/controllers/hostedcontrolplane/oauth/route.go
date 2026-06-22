package oauth

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/netutil"

	routev1 "github.com/openshift/api/route/v1"
)

func ReconcileExternalPublicRoute(route *routev1.Route, ownerRef config.OwnerRef, hostname string, defaultIngressDomain string, labelHCPRoutes bool) error {
	ownerRef.ApplyTo(route)
	return netutil.ReconcileExternalRoute(route, hostname, defaultIngressDomain, manifests.OauthServerService(route.Namespace).Name, labelHCPRoutes)
}

func ReconcileExternalPrivateRoute(route *routev1.Route, ownerRef config.OwnerRef, hostname string, defaultIngressDomain string, labelHCPRoutes bool) error {
	ownerRef.ApplyTo(route)
	if err := netutil.ReconcileExternalRoute(route, hostname, defaultIngressDomain, manifests.OauthServerService(route.Namespace).Name, labelHCPRoutes); err != nil {
		return err
	}
	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Labels[hyperv1.RouteVisibilityLabel] = hyperv1.RouteVisibilityPrivate
	netutil.AddInternalRouteLabel(route)
	return nil
}

func ReconcileInternalRoute(route *routev1.Route, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(route)
	// Assumes ownerRef is the HCP
	return netutil.ReconcileInternalRoute(route, ownerRef.Reference.Name, manifests.OauthServerService(route.Namespace).Name)
}
