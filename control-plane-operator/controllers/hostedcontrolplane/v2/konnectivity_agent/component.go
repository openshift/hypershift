package konnectivity

import (
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "konnectivity-agent"
)

var _ component.ComponentOptions = &konnectivityAgent{}

type konnectivityAgent struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *konnectivityAgent) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *konnectivityAgent) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *konnectivityAgent) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &konnectivityAgent{}).
		WithAdaptFunction(adaptDeployment).
		WithDependencies(kasv2.ComponentName).
		Build()
}
