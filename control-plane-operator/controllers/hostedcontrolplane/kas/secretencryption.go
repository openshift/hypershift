package kas

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
