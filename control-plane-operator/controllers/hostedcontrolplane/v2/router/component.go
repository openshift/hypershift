package router

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	endpointresolverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/endpoint_resolver"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/router/util"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"

	routev1 "github.com/openshift/api/route/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "router"
)

var _ component.ComponentOptions = &router{}

type router struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *router) IsRequestServing() bool {
	return true
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *router) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *router) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &router{}).
		WithPredicate(routerPredicate).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithDependencies(ignitionserverv2.ComponentName).
		Build()
}

func routerPredicate(cpContext component.WorkloadContext) (bool, error) {
	if !util.UseHCPRouter(cpContext.HCP) {
		return false, nil
	}
	if azureutil.IsAroHCPByHCP(cpContext.HCP) {
		if err := ensureHCPRouterRoutesExist(cpContext); err != nil {
			return false, err
		}
	}
	return true, nil
}

// TODO: introduce live reloading like in shared proxy so the router config
// is updated when routes change after the initial reconcile.

func ensureHCPRouterRoutesExist(cpContext component.WorkloadContext) error {
	expected := aroExpectedHCPRouterRouteNames(cpContext.HCP)
	if len(expected) == 0 {
		return nil
	}

	routeList := &routev1.RouteList{}
	if err := cpContext.Client.List(cpContext, routeList, client.InNamespace(cpContext.HCP.Namespace)); err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	routesByName := make(map[string]routev1.Route, len(routeList.Items))
	for _, route := range routeList.Items {
		routesByName[route.Name] = route
	}

	var missing []string
	for _, name := range expected {
		route, ok := routesByName[name]
		if !ok || !hcpRouterRouteReady(&route) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("waiting for HCP router routes: %s", strings.Join(missing, ", "))
	}
	return nil
}

var aroBaseHCPRouterRouteNames = []string{
	"kube-apiserver-internal",
	"konnectivity-server",
	"oauth-internal",
	"ignition-server",
}

func aroExpectedHCPRouterRouteNames(hcp *hyperv1.HostedControlPlane) []string {
	if !azureutil.IsAroHCPByHCP(hcp) {
		return nil
	}

	names := append([]string(nil), aroBaseHCPRouterRouteNames...)
	if metricsProxyRouteRequired(hcp) {
		names = append(names, "metrics-proxy")
	}
	return names
}

func metricsProxyRouteRequired(hcp *hyperv1.HostedControlPlane) bool {
	enabled, err := endpointresolverv2.Predicate(component.WorkloadContext{HCP: hcp})
	if err != nil || !enabled {
		return false
	}
	return netutil.LabelHCPRoutes(hcp) || netutil.IsPrivateHCP(hcp)
}

func hcpRouterRouteReady(route *routev1.Route) bool {
	return route.Spec.Host != ""
}
