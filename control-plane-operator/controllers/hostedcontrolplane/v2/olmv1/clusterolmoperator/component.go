package clusterolmoperator

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "cluster-olm-operator"
)

var _ component.ComponentOptions = &clusterOLMOperator{}

type clusterOLMOperator struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *clusterOLMOperator) IsRequestServing() bool {
	return false // Internal operator, not externally accessible
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *clusterOLMOperator) MultiZoneSpread() bool {
	return true // Should spread across zones for HA
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *clusterOLMOperator) NeedsManagementKASAccess() bool {
	return true // Needs management cluster access for ClusterOperator status reporting
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterOLMOperator{}).
		WithAdaptFunction(adaptDeployment).
		InjectAvailabilityProberContainer(podspec.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterCatalog"},
				{Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterExtension"},
			},
		}).
		Build()
}
