package azure

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "azure-cloud-controller-manager"
)

var _ component.ComponentOptions = &azureOptions{}

type azureOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &azureOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"serviceaccount.yaml",
			component.WithAdaptFunction(adaptServiceAccount),
		).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"config-secret.yaml",
			component.WithAdaptFunction(adaptConfigSecret),
		).
		WithManifestAdapter(
			"config-secretprovider.yaml",
			component.WithAdaptFunction(adaptSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountNameSpace: "kube-system",
			ServiceAccountName:      "azure-cloud-provider",
		}).
		Build()

}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.AzurePlatform, nil
}

func adaptServiceAccount(cpContext component.WorkloadContext, sa *corev1.ServiceAccount) error {
	// Add Azure Workload Identity annotations
	if sa.Annotations == nil {
		sa.Annotations = make(map[string]string)
	}

	// Get the client ID from the HostedControlPlane spec
	clientID := cpContext.HCP.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.CloudProvider.ClientID
	tenantID := cpContext.HCP.Spec.Platform.Azure.TenantID

	sa.Annotations["azure.workload.identity/client-id"] = string(clientID)
	sa.Annotations["azure.workload.identity/tenant-id"] = tenantID

	return nil
}

func isAroHCP(cpContext component.WorkloadContext) bool {
	return azureutil.IsAroHCP()
}
