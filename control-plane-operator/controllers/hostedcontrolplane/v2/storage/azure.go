package storage

import (
	"encoding/json"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/secretproviderclass"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/azure"
	hyperconfig "github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func adaptAzureCSISecret(cpContext component.WorkloadContext, managedIdentity hyperv1.ManagedIdentity, secret *corev1.Secret) error {
	// Get the credentials secret so we can retrieve the tenant ID for the configuration
	credentialsSecret := manifests.AzureCredentialInformation(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return fmt.Errorf("failed to get Azure credentials secret: %w", err)
	}

	tenantID := string(credentialsSecret.Data["AZURE_TENANT_ID"])
	azureSpec := cpContext.HCP.Spec.Platform.Azure

	azureConfig := azure.AzureConfig{
		Cloud:             azureSpec.Cloud,
		TenantID:          tenantID,
		SubscriptionID:    azureSpec.SubscriptionID,
		ResourceGroup:     azureSpec.ResourceGroupName,
		Location:          azureSpec.Location,
		AADClientID:       managedIdentity.ClientID,
		AADClientCertPath: path.Join(hyperconfig.ManagedAzureCertificatePath, managedIdentity.CertificateName),
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
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity.CertificateName)
	return nil
}

func adaptAzureCSIFileSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File
	return adaptAzureCSISecret(cpContext, managedIdentity, secret)
}

func adaptAzureCSIFileSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity.CertificateName)
	return nil
}
