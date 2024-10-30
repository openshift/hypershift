package aws

import (
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

var _ component.ComponentOptions = &awsOptions{}

type awsOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *awsOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *awsOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *awsOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponentBuilder(componentName string) *component.ControlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return component.NewDeploymentComponent(componentName, &awsOptions{})
}
