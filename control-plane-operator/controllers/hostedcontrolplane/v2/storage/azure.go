package storage

import (
	"encoding/json"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/azure"
	hyperazureutil "github.com/openshift/hypershift/support/azureutil"
	hyperconfig "github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/secretproviderclass"

	corev1 "k8s.io/api/core/v1"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func adaptAzureCSISecret(cpContext component.WorkloadContext, managedIdentity hyperv1.ManagedIdentity, secret *corev1.Secret) error {
	azureSpec := cpContext.HCP.Spec.Platform.Azure

	azureConfig := azure.AzureConfig{
		Cloud:                       azureSpec.Cloud,
		TenantID:                    azureSpec.TenantID,
		SubscriptionID:              azureSpec.SubscriptionID,
		ResourceGroup:               azureSpec.ResourceGroupName,
		Location:                    azureSpec.Location,
		AADMSIDataPlaneIdentityPath: path.Join(hyperconfig.ManagedAzureCertificatePath, managedIdentity.CredentialsSecretName),
	}

	var getVnetNameAndResourceGroupErr error
	// aro hcp csi nfs protocol provision volumes needs the vnetName/vnetResourceGroup config
	azureConfig.VnetName, azureConfig.VnetResourceGroup, getVnetNameAndResourceGroupErr = hyperazureutil.GetVnetNameAndResourceGroupFromVnetID(azureSpec.VnetID)
	if getVnetNameAndResourceGroupErr != nil {
		return fmt.Errorf("failed to get vnet info: %w", getVnetNameAndResourceGroupErr)
	}

	serializedConfig, err := json.MarshalIndent(azureConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	secret.Data[azure.ConfigKey] = serializedConfig
	return nil
}

func adaptAzureCSIDiskSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Disk
	return adaptAzureCSISecret(cpContext, managedIdentity, secret)
}

func adaptAzureCSIDiskSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Disk
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity, true)
	return nil
}

func adaptAzureCSIFileSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File
	return adaptAzureCSISecret(cpContext, managedIdentity, secret)
}

func adaptAzureCSIFileSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity)
	return nil
}
