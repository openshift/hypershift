package kcm

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	ComponentName = "kube-controller-manager"
)

var _ component.ComponentOptions = &KubeControllerManager{}

type KubeControllerManager struct {
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &KubeControllerManager{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"kcm-config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"recycler-config.yaml",
			component.WithAdaptFunction(adaptRecyclerConfig),
		).
		WithManifestAdapter(
			"kubeconfig.yaml",
			component.WithAdaptFunction(adaptKubeconfig),
		).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
		).
		RolloutOnConfigMapChange("kcm-config", manifests.RootCAConfigMap("").Name, manifests.ServiceServingCA("").Name).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *KubeControllerManager) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *KubeControllerManager) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *KubeControllerManager) NeedsManagementKASAccess() bool {
	return false
}
