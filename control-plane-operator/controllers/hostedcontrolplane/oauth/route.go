package oauth

import (
	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileExternalRoute(route *routev1.Route, ownerRef config.OwnerRef, hostname string, defaultIngressDomain string) error {
	ownerRef.ApplyTo(route)
	return util.ReconcileExternalRoute(route, hostname, defaultIngressDomain, manifests.OauthServerService(route.Namespace).Name)
}

func ReconcileInternalRoute(route *routev1.Route, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(route)
	// Assumes ownerRef is the HCP
	return util.ReconcileInternalRoute(route, ownerRef.Reference.Name, manifests.OauthServerService(route.Namespace).Name)
}
