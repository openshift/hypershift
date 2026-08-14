package kubestorageversionmigrator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "kube-storage-version-migrator"
)

var _ component.ComponentOptions = &migratorOptions{}

type migratorOptions struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (o *migratorOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (o *migratorOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (o *migratorOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &migratorOptions{}).
		WithPredicate(predicate).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	p := cpContext.HCP.Spec.Platform.Type
	if p == hyperv1.IBMCloudPlatform || p == hyperv1.PowerVSPlatform {
		return false, nil
	}
	return cpContext.HCP.Spec.SecretEncryption != nil, nil
}
