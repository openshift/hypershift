package router

import (
	"fmt"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	endpointresolverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/endpoint_resolver"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/router/util"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"
	supportutil "github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "router"
)

var _ component.ComponentOptions = &router{}

type router struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
// Although the router does serve requests, returning false here is intentional:
// with 2 replicas, IsRequestServing=true causes SetReplicasAndStrategy to set
// maxUnavailable=1, which allows a stuck replacement pod to reduce available
// replicas to 0. Returning false keeps maxUnavailable=0 so at least one pod
// remains available throughout the rolling update.
func (k *router) IsRequestServing() bool {
	return false
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

	// Also verify that each route's backend Service has a ClusterIP assigned.
	// If the Service exists but Kubernetes has not yet allocated a ClusterIP, the
	// router ConfigMap would be generated with an empty destination IP and later
	// updated once the ClusterIP is available, triggering an unnecessary rolling
	// update of the router pods at a time when they are susceptible to Azure CNI
	// DHCP timeouts.
	expectedSet := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		expectedSet[name] = struct{}{}
	}
	var missingIPs []string
	for _, route := range routesByName {
		if _, ok := expectedSet[route.Name]; !ok {
			continue
		}
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      route.Spec.To.Name,
				Namespace: cpContext.HCP.Namespace,
			},
		}
		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(svc), svc); err != nil {
			return fmt.Errorf("failed to get service %s for route %s: %w", route.Spec.To.Name, route.Name, err)
		}
		if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
			missingIPs = append(missingIPs, route.Spec.To.Name)
		}
	}
	if len(missingIPs) > 0 {
		sort.Strings(missingIPs)
		return fmt.Errorf("waiting for ClusterIP on services: %s", strings.Join(missingIPs, ", "))
	}

	return nil
}

var aroBaseHCPRouterRouteNames = []string{
	"kube-apiserver-internal",
	"konnectivity-server",
	"ignition-server",
}

func aroExpectedHCPRouterRouteNames(hcp *hyperv1.HostedControlPlane) []string {
	if !azureutil.IsAroHCPByHCP(hcp) {
		return nil
	}

	names := append([]string(nil), aroBaseHCPRouterRouteNames...)
	if supportutil.HCPOAuthEnabled(hcp) {
		names = append(names, "oauth-internal")
	}
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
