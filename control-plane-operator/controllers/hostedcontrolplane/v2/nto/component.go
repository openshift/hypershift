package nto

import (
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "cluster-node-tuning-operator"
)

var _ component.ComponentOptions = &clusterNodeTuningOperator{}

type clusterNodeTuningOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *clusterNodeTuningOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *clusterNodeTuningOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *clusterNodeTuningOperator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterNodeTuningOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
		).
		WithDependencies(oapiv2.ComponentName).
		Build()
}
