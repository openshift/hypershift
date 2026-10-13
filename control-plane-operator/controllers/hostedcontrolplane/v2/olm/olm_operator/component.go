package olmoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "olm-operator"
)

var _ component.ComponentOptions = &olmOperator{}

type olmOperator struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *olmOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *olmOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *olmOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &olmOperator{}).
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
				{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"},
				{Group: "operators.coreos.com", Version: "v2", Kind: "OperatorCondition"},
				{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"},
				{Group: "operators.coreos.com", Version: "v1", Kind: "OLMConfig"},
			},
		}).
		Build()
}
