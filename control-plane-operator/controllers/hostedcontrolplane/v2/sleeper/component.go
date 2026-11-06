package sleeper

import component "github.com/openshift/hypershift/support/controlplane-component"

const ComponentName = "cpo-sleeper"

var _ component.ComponentOptions = &sleeper{}

type sleeper struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (*sleeper) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (*sleeper) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (*sleeper) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &sleeper{}).
		Build()
}
