package kas

import (
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

const (
	secretEncryptionConfigurationKey = "config.yaml"
	encryptionConfigurationKind      = "EncryptionConfiguration"
)

func ReconcileKMSEncryptionConfig(config *corev1.Secret,
	ownerRef config.OwnerRef,
	encryptionSpec *hyperv1.KMSSpec,
) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string][]byte{}
	}

	encryptionConfigurationBytes, err := generateKMSEncryptionConfig(config, encryptionSpec)
	if err != nil {
		return err
	}

	config.Data[secretEncryptionConfigurationKey] = encryptionConfigurationBytes
	return nil
}

func ReconcileAESCBCEncryptionConfig(config *corev1.Secret,
	ownerRef config.OwnerRef,
	activeKey []byte,
	backupKey []byte,
) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string][]byte{}
	}
	encryptionConfigurationBytes, err := generateAESCBCEncryptionConfig(activeKey, backupKey)
	if err != nil {
		return err
	}
	config.Data[secretEncryptionConfigurationKey] = encryptionConfigurationBytes
	return nil
}

func kasVolumeSecretEncryptionConfigFile() *corev1.Volume {
	return &corev1.Volume{
		Name: "kas-secret-encryption-config",
	}
}

func buildVolumeSecretEncryptionConfigFile(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASSecretEncryptionConfigFile("").Name
}

func ReconcileKMSConfigSecret(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane) error {
	azureConfig := azure.AzureConfig{
		Cloud:                        hcp.Spec.Platform.Azure.Cloud,
		TenantID:                     hcp.Spec.Platform.Azure.TenantID,
		UseManagedIdentityExtension:  false,
		SubscriptionID:               hcp.Spec.Platform.Azure.SubscriptionID,
		ResourceGroup:                hcp.Spec.Platform.Azure.ResourceGroupName,
		Location:                     hcp.Spec.Platform.Azure.Location,
		LoadBalancerName:             hcp.Spec.InfraID,
		CloudProviderBackoff:         true,
		CloudProviderBackoffDuration: 6,
		UseInstanceMetadata:          false,
		LoadBalancerSku:              "standard",
		DisableOutboundSNAT:          true,
		AADMSIDataPlaneIdentityPath:  config.ManagedAzureCertificatePath + hcp.Spec.SecretEncryption.KMS.Azure.KMS.CredentialsSecretName,
	}

	serializedConfig, err := json.MarshalIndent(azureConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[azure.CloudConfigKey] = serializedConfig

	return nil
}
