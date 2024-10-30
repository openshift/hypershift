package openstack

import (
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

var _ component.ComponentOptions = &openstackOptions{}

type openstackOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *openstackOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *openstackOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *openstackOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponentBuilder(componentName string) *component.ControlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return component.NewDeploymentComponent(componentName, &openstackOptions{}).
		WithAdaptFunction(adaptDeployment)
}
