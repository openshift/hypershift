package catalogd

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "catalogd"
)

var _ component.ComponentOptions = &catalogd{}

type catalogd struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *catalogd) IsRequestServing() bool {
	return false // Internal service, not externally accessible
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *catalogd) MultiZoneSpread() bool {
	return false // Single replica per hosted cluster
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *catalogd) NeedsManagementKASAccess() bool {
	return false // Only needs hosted cluster API access
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &catalogd{}).
		WithAdaptFunction(adaptDeployment).
		WithDependencies("kube-apiserver"). // Wait for hosted API server
		RolloutOnSecretChange("admin-kubeconfig").
		WithManifestAdapter(
			"service.yaml",
			component.WithAdaptFunction(adaptService),
		).
		InjectAvailabilityProberContainer(component.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterCatalog"},
			},
		}).
		Build()
}
