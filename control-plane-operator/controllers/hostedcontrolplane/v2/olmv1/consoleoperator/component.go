package consoleoperator

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "console-operator"
)

var _ component.ComponentOptions = &consoleOperator{}

type consoleOperator struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *consoleOperator) IsRequestServing() bool {
	return false // Internal operator, not externally accessible
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *consoleOperator) MultiZoneSpread() bool {
	return true // Should spread across zones for HA
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *consoleOperator) NeedsManagementKASAccess() bool {
	return false // Only needs hosted cluster API access via in-cluster config
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &consoleOperator{}).
		WithAdaptFunction(adaptDeployment).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Socks5,
		}).
		InjectAvailabilityProberContainer(podspec.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "", Version: "v1", Kind: "Service"},
				{Group: "", Version: "v1", Kind: "ConfigMap"},
			},
		}).
		Build()
}
