package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/support/azureutil"
	hypershiftconfig "github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
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
	config.AADClientID = hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Disk.ClientID
	config.AADClientCertPath = path.Join(hypershiftconfig.ManagedAzureCertificatePath, hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Disk.CertificateName)

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
func ReconcileAzureFileCSISecret(ctx context.Context, secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, tenantID string) error {
	config := initializeAzureCSIControllerConfig(hcp, tenantID)
	config.AADClientID = hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File.ClientID
	config.AADClientCertPath = path.Join(hypershiftconfig.ManagedAzureCertificatePath, hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File.CertificateName)

	// Get the VNET name from the VNET ID for the CSI File configuration
	creds, err := azureutil.GetAzureCredsForCPOManagedIdentity(hcp, tenantID)
	if err != nil {
		return fmt.Errorf("failed to create azure creds to verify resource group locations: %v", err)
	}
	vnet, err := azureutil.GetVnetInfoFromVnetID(ctx, hcp.Spec.Platform.Azure.VnetID, hcp.Spec.Platform.Azure.SubscriptionID, creds)
	if err != nil {
		return err
	}
	config.VnetName = ptr.Deref(vnet.Name, "")

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
