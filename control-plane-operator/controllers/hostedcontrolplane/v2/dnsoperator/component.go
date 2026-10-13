package dnsoperator

import (
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
		InjectServiceAccountKubeConfig(component.ServiceAccountKubeConfigOpts{
			Name:      "dns-operator",
			Namespace: "openshift-dns-operator",
			MountPath: "/etc/kubernetes",
		}).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: component.ServiceAccountKubeconfigVolumeName,
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "DNS"},
			},
			WaitForLabeledPodsGone: "openshift-dns-operator/name=dns-operator",
		}).
		Build()
}
