package storage

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "cluster-storage-operator"
)

var _ component.ComponentOptions = &clusterStorageOperator{}

type clusterStorageOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *clusterStorageOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *clusterStorageOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *clusterStorageOperator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterStorageOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isStorageAndCSIManaged).
		WithManifestAdapter(
			"azure-disk-csi-config.yaml",
			component.WithAdaptFunction(adaptAzureCSIDiskSecret),
			component.WithPredicate(isAroHCP),
		).
		WithManifestAdapter(
			"azure-disk-csi-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureCSIDiskSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithManifestAdapter(
			"azure-file-csi-config.yaml",
			component.WithAdaptFunction(adaptAzureCSIFileSecret),
			component.WithPredicate(isAroHCP),
		).
		WithManifestAdapter(
			"azure-file-csi-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureCSIFileSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "guest-kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "Storage"},
			},
		}).
		Build()
}

func isStorageAndCSIManaged(cpContext component.WorkloadContext) (bool, error) {
	if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform || cpContext.HCP.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		return false, nil
	}
	return true, nil
}

func isAroHCP(cpContext component.WorkloadContext) bool {
	return azureutil.IsAroHCP()
}
