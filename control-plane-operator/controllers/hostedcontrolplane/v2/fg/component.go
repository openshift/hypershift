package fg

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "featuregate-generator"
)

var _ component.ComponentOptions = &FeatureGateGenerator{}

type FeatureGateGenerator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (g *FeatureGateGenerator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (g *FeatureGateGenerator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (*FeatureGateGenerator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewJobComponent(ComponentName, &FeatureGateGenerator{}).
		WithAdaptFunction(adaptJob).
		Build()
}
