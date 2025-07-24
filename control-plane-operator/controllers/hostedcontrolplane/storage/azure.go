package storage

import (
	"encoding/json"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	hyperazureutil "github.com/openshift/hypershift/support/azureutil"
	hypershiftconfig "github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

// initializeAzureCSIControllerConfig initializes an AzureConfig object which will be used to populate the secrets
// needed by azure-disk-csi-controller and azure-file-csi-controller.
func initializeAzureCSIControllerConfig(hcp *hyperv1.HostedControlPlane, tenantID string) azure.AzureConfig {
	azureConfig := azure.AzureConfig{
		Cloud:          hcp.Spec.Platform.Azure.Cloud,
		TenantID:       tenantID,
		SubscriptionID: hcp.Spec.Platform.Azure.SubscriptionID,
		ResourceGroup:  hcp.Spec.Platform.Azure.ResourceGroupName,
		Location:       hcp.Spec.Platform.Azure.Location,
	}

	return azureConfig
}

// ReconcileAzureDiskCSISecret reconciles the configuration for the secret as expected by azure-disk-csi-controller
func ReconcileAzureDiskCSISecret(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, tenantID string) error {
	config := initializeAzureCSIControllerConfig(hcp, tenantID)
	config.AADMSIDataPlaneIdentityPath = path.Join(hypershiftconfig.ManagedAzureCertificatePath, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Disk.CredentialsSecretName)

	serializedConfig, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[azure.CloudConfigKey] = serializedConfig
	return nil
}

// ReconcileAzureFileCSISecret reconciles the configuration for the secret as expected by azure-file-csi-controller
func ReconcileAzureFileCSISecret(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, tenantID string) error {
	config := initializeAzureCSIControllerConfig(hcp, tenantID)
	config.AADMSIDataPlaneIdentityPath = path.Join(hypershiftconfig.ManagedAzureCertificatePath, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.File.CredentialsSecretName)

	var getVnetNameAndResourceGroupErr error
	// aro hcp csi nfs protocol provision volumes needs the vnetName/vnetResourceGroup config
	config.VnetName, config.VnetResourceGroup, getVnetNameAndResourceGroupErr = hyperazureutil.GetVnetNameAndResourceGroupFromVnetID(hcp.Spec.Platform.Azure.VnetID)
	if getVnetNameAndResourceGroupErr != nil {
		return fmt.Errorf("failed to get vnet info: %w", getVnetNameAndResourceGroupErr)
	}

	serializedConfig, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[azure.CloudConfigKey] = serializedConfig
	return nil
}
