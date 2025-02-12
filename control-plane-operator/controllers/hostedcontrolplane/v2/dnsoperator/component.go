package dnsoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "dns-operator"
)

var _ component.ComponentOptions = &dnsOperator{}

type dnsOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (d *dnsOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (d *dnsOperator) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (d *dnsOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &dnsOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"kubeconfig.yaml",
			component.WithAdaptFunction(adaptKubeconfigSecret),
			component.DisableIfAnnotationExist(hyperv1.DisablePKIReconciliationAnnotation),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "dns-operator-kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "DNS"},
			},
			WaitForLabeledPodsGone: "openshift-dns-operator/name=dns-operator",
		}).
		Build()
}
