package scheduler

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "kube-scheduler"
)

var _ component.ComponentOptions = &kubeScheduler{}

type kubeScheduler struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *kubeScheduler) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *kubeScheduler) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *kubeScheduler) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &kubeScheduler{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfigMap),
		).
		WithManifestAdapter(
			"kubeconfig.yaml",
			component.WithAdaptFunction(adaptKubeconfig),
		).
		WatchResource(&corev1.ConfigMap{}, "kube-scheduler").
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}
