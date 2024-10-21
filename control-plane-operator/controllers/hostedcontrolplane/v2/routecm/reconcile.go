package routecm

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	component "github.com/openshift/hypershift/support/controlplane-component"
	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "openshift-route-controller-manager"

	ConfigMapName = "openshift-route-controller-manager-config"
)

var _ component.ComponentOptions = &RouteController{}

type RouteController struct {
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &RouteController{}).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfigMap),
		).
		WithManifestAdapter(
			"service.yaml",
			component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices),
		).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
			component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices),
		).
		WatchResource(&corev1.ConfigMap{}, ConfigMapName).
		Build()
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *RouteController) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *RouteController) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *RouteController) NeedsManagementKASAccess() bool {
	return false
}
