package etcd

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "etcd"
)

var _ component.ComponentOptions = &etcd{}

type etcd struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (e *etcd) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (e *etcd) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
// etcd needs management kas access for the etcd-defrag-controller to implement leader election.
func (e *etcd) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewStatefulSetComponent(ComponentName, &etcd{}).
		WithAdaptFunction(adaptStatefulSet).
		WithPredicate(isManagedETCD).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithManifestAdapter(
			"defrag-role.yaml",
			component.WithPredicate(defragControllerPredicate),
		).
		WithManifestAdapter(
			"defrag-rolebinding.yaml",
			component.WithPredicate(defragControllerPredicate),
		).
		WithManifestAdapter(
			"defrag-serviceaccount.yaml",
			component.WithPredicate(defragControllerPredicate),
		).
		Build()
}

func isManagedETCD(cpContext component.WorkloadContext) (bool, error) {
	managed := cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Managed
	return managed, nil
}

// Only deploy etcd-defrag-controller in HA mode.
// When we perform defragmentation it takes the etcd instance offline for a short amount of time.
// Therefore we only want to do this when there are multiple etcd instances.
func defragControllerPredicate(cpContext component.WorkloadContext) bool {
	return cpContext.HCP.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable
}
