package configoperator

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "hosted-cluster-config-operator"
)

var _ component.ComponentOptions = &hcco{}

type hcco struct {
	registryOverrides               map[string]string
	openShiftImageRegistryOverrides map[string][]string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (h *hcco) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (h *hcco) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (h *hcco) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(registryOverrides map[string]string, openShiftImageRegistryOverrides map[string][]string) component.ControlPlaneComponent {
	hcco := &hcco{
		registryOverrides:               registryOverrides,
		openShiftImageRegistryOverrides: openShiftImageRegistryOverrides,
	}

	return component.NewDeploymentComponent(ComponentName, hcco).
		WithAdaptFunction(hcco.adaptDeployment).
		WithManifestAdapter(
			"podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithManifestAdapter(
			"role.yaml",
			component.WithAdaptFunction(adaptRole),
		).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "imageregistry.operator.openshift.io", Version: "v1", Kind: "Config"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Infrastructure"},
				{Group: "config.openshift.io", Version: "v1", Kind: "DNS"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Ingress"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Network"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Proxy"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Build"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Image"},
				{Group: "config.openshift.io", Version: "v1", Kind: "Project"},
				{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"},
				{Group: "config.openshift.io", Version: "v1", Kind: "FeatureGate"},
				{Group: "config.openshift.io", Version: "v1", Kind: "ClusterOperator"},
				{Group: "config.openshift.io", Version: "v1", Kind: "OperatorHub"},
				{Group: "operator.openshift.io", Version: "v1", Kind: "Network"},
				{Group: "operator.openshift.io", Version: "v1", Kind: "CloudCredential"},
				{Group: "operator.openshift.io", Version: "v1", Kind: "IngressController"},
			},
		}).
		Build()
}
