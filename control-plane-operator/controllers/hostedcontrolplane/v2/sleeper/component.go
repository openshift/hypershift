package sleeper

import component "github.com/openshift/hypershift/support/controlplane-component"

const (
	ComponentName    = "cpo-sleeper"
	EnableAnnotation = "hypershift.openshift.io/enable-cpo-sleeper"
)

var _ component.ComponentOptions = &sleeper{}

type sleeper struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (s *sleeper) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (s *sleeper) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (s *sleeper) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &sleeper{}).
		WithPredicate(predicate).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Annotations[EnableAnnotation] == "true", nil
}
