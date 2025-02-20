package catalogoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "catalog-operator"
)

var _ component.ComponentOptions = &catalogOperator{}

type catalogOperator struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *catalogOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *catalogOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *catalogOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &catalogOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"metrics-service.yaml",
			component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices),
		).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
			component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices),
		).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Socks5,
		}).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "CatalogSource"},
			},
		}).
		Build()
}
