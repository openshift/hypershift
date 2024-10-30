package azure

import (
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

var _ component.ComponentOptions = &azureOptions{}

type azureOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponentBuilder(componentName string) *component.ControlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return component.NewDeploymentComponent(componentName, &azureOptions{}).
		WithAdaptFunction(adaptDeployment)
}
