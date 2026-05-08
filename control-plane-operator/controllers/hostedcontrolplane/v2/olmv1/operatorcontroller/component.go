package operatorcontroller

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "operator-controller"
)

var _ component.ComponentOptions = &operatorController{}

type operatorController struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (oc *operatorController) IsRequestServing() bool {
	return false // Internal controller, not externally serving
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (oc *operatorController) MultiZoneSpread() bool {
	return false // Single replica per hosted cluster
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (oc *operatorController) NeedsManagementKASAccess() bool {
	return false // Uses hosted cluster API only (not management cluster API)
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &operatorController{}).
		WithAdaptFunction(adaptDeployment).
		WithDependencies("catalogd", "kube-apiserver").
		RolloutOnSecretChange("admin-kubeconfig").
		InjectAvailabilityProberContainer(component.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterExtension"},
			},
		}).
		Build()
}
