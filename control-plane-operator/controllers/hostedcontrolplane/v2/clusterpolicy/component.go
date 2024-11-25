package clusterpolicy

import (
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	ComponentName = "cluster-policy-controller"
)

var _ component.ComponentOptions = &clusterPolicyController{}

type clusterPolicyController struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *clusterPolicyController) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *clusterPolicyController) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *clusterPolicyController) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterPolicyController{}).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfigMap),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}
