package storage

import (
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"

	corev1 "k8s.io/api/core/v1"
)

// initializeAzureCSIControllerConfig initializes an AzureConfig object which will be used to populate the secrets
// needed by azure-disk-csi-controller and azure-file-csi-controller.
// The source of truth for which fields are required by azure-disk is located here - https://github.com/kubernetes-sigs/azuredisk-csi-driver/blob/master/deploy/example/azure.json.
// The source of truth for which fields are required by azure-file is located here - https://github.com/kubernetes-sigs/azurefile-csi-driver/blob/master/deploy/example/azure.json.
// As of Sept 2024, the fields we need in HyperShift are the same between both of these sources of truth.
func initializeAzureCSIControllerConfig(hcp *hyperv1.HostedControlPlane, tenantID string) azure.AzureConfig {
	azureConfig := azure.AzureConfig{
		// These fields are mandatory
		Cloud:          hcp.Spec.Platform.Azure.Cloud,
		TenantID:       tenantID,
		SubscriptionID: hcp.Spec.Platform.Azure.SubscriptionID,
		ResourceGroup:  hcp.Spec.Platform.Azure.ResourceGroupName,
		Location:       hcp.Spec.Platform.Azure.Location,

		// These fields are mandatory when using managed identity; the user assigned identity ID is populated after this function call
		UseManagedIdentityExtension: true,
		UserAssignedIdentityID:      "",
	}

	return azureConfig
}

// ReconcileAzureDiskCSISecret reconciles the configuration for the secret as expected by azure-disk-csi-controller
func ReconcileAzureDiskCSISecret(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, tenantID string) error {
	config := initializeAzureCSIControllerConfig(hcp, tenantID)
	config.UserAssignedIdentityID = string(hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlaneManagedIdentities.AzureDiskManagedIdentityClientID)

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
	config.UserAssignedIdentityID = string(hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlaneManagedIdentities.AzureFileManagedIdentityClientID)

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
