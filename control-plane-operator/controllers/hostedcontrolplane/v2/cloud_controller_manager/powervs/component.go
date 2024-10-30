package powervs

import (
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

var _ component.ComponentOptions = &powervsOptions{}

type powervsOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *powervsOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *powervsOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *powervsOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponentBuilder(componentName string) *component.ControlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return component.NewDeploymentComponent(componentName, &powervsOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		)
}
