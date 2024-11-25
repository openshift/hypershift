package ocm

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "openshift-controller-manager"

	configMapName = "openshift-controller-manager-config"
)

var _ component.ComponentOptions = &openshiftControllerManager{}

type openshiftControllerManager struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *openshiftControllerManager) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *openshiftControllerManager) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *openshiftControllerManager) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &openshiftControllerManager{}).
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
		WithDependencies(oapiv2.ComponentName).
		WatchResource(&corev1.ConfigMap{}, configMapName).
		Build()
}
