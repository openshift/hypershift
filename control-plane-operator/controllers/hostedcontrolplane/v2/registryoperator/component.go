package registryoperator

import (
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "cluster-image-registry-operator"
)

var _ component.ComponentOptions = &imageRegistryOperator{}

type imageRegistryOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *imageRegistryOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *imageRegistryOperator) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *imageRegistryOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &imageRegistryOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isImageRegistryCapabilityEnabled).
		WithManifestAdapter(
			"podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithManifestAdapter(
			"azure-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudAndAPIServerToken,
			ServiceAccountName:      "cluster-image-registry-operator",
			ServiceAccountNameSpace: "openshift-image-registry",
		}).
		Build()
}

func isImageRegistryCapabilityEnabled(cpContext component.WorkloadContext) (bool, error) {
	return capabilities.IsImageRegistryCapabilityEnabled(
		cpContext.HCP.Spec.Capabilities,
	), nil
}

func isAroHCP(cpContext component.WorkloadContext) bool {
	return azureutil.IsAroHCP()
}
