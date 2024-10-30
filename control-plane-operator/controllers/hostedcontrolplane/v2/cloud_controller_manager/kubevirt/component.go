package kubevirt

import (
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

var _ component.ComponentOptions = &kubevirtOptions{}

type kubevirtOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *kubevirtOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *kubevirtOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *kubevirtOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponentBuilder(componentName string) *component.ControlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return component.NewDeploymentComponent(componentName, &kubevirtOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"kubevirt-cloud-config.yaml",
			component.WithAdaptFunction(adaptConfig),
		)
}
