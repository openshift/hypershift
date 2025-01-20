package kubevirt

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "kubevirt-csi-controller"
)

var _ component.ComponentOptions = &kubevirtCSIOptions{}

type kubevirtCSIOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *kubevirtCSIOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *kubevirtCSIOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *kubevirtCSIOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &kubevirtCSIOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"kubeconfig.yaml",
			component.WithAdaptFunction(adaptKubeconfigSecret),
		).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfigMap),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	if cpContext.HCP.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return false, nil
	}
	// Do not install kubevirt-csi if the storage driver is set to NONE
	if getStorageDriverType(cpContext.HCP) == hyperv1.NoneKubevirtStorageDriverConfigType {
		return false, nil
	}

	return true, nil
}

func getStorageDriverType(hcp *hyperv1.HostedControlPlane) hyperv1.KubevirtStorageDriverConfigType {
	storageDriverType := hyperv1.DefaultKubevirtStorageDriverConfigType

	if hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.StorageDriver != nil &&
		hcp.Spec.Platform.Kubevirt.StorageDriver.Type != "" {

		storageDriverType = hcp.Spec.Platform.Kubevirt.StorageDriver.Type
	}
	return storageDriverType
}
