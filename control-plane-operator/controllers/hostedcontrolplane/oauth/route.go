package oauth

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileExternalPublicRoute(route *routev1.Route, ownerRef config.OwnerRef, hostname string, defaultIngressDomain string) error {
	ownerRef.ApplyTo(route)
	return util.ReconcileExternalRoute(route, hostname, defaultIngressDomain, manifests.OauthServerService(route.Namespace).Name)
}

func ReconcileExternalPrivateRoute(route *routev1.Route, ownerRef config.OwnerRef, hostname string, defaultIngressDomain string) error {
	ownerRef.ApplyTo(route)
	if err := util.ReconcileExternalRoute(route, hostname, defaultIngressDomain, manifests.OauthServerService(route.Namespace).Name); err != nil {
		return err
	}
	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Labels[hyperv1.RouteVisibilityLabel] = hyperv1.RouteVisibilityPrivate
	util.AddInternalRouteLabel(route)
	return nil
}

func ReconcileInternalRoute(route *routev1.Route, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(route)
	// Assumes ownerRef is the HCP
	return util.ReconcileInternalRoute(route, ownerRef.Reference.Name, manifests.OauthServerService(route.Namespace).Name)
}
