package router

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/router/util"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "router"
)

var _ component.ComponentOptions = &router{}

type router struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *router) IsRequestServing() bool {
	return true
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *router) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *router) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &router{}).
		WithPredicate(func(cpContext component.WorkloadContext) (bool, error) {
			return util.UseHCPRouter(cpContext.HCP), nil
		}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		Build()
}
