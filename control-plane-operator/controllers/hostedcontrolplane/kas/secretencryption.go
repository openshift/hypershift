package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	hcpconfig "github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
)

const (
	secretEncryptionConfigurationKey = "config.yaml"
	encryptionConfigurationKind      = "EncryptionConfiguration"
)

func ReconcileKMSEncryptionConfig(config *corev1.Secret,
	ownerRef hcpconfig.OwnerRef,
	encryptionSpec *hyperv1.KMSSpec,
) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string][]byte{}
	}
	var encryptionConfigurationBytes []byte
	switch encryptionSpec.Provider {
	case hyperv1.IBMCloud:
		// build encryption config map
		if encryptionSpec.IBMCloud == nil {
			return fmt.Errorf("ibmcloud kms key metadata not specified")
		}
		ibmCloudKMSEncryptionConfigBytes, err := generateIBMCloudKMSEncryptionConfig(encryptionSpec.IBMCloud.KeyList)
		if err != nil {
			return err
		}
		encryptionConfigurationBytes = ibmCloudKMSEncryptionConfigBytes
	case hyperv1.AWS:
		if encryptionSpec.AWS == nil {
			return fmt.Errorf("aws kms key metadata not specified")
		}
		awsKMSEncryptionConfigBytes, err := generateAWSKMSEncryptionConfig(encryptionSpec.AWS.ActiveKey, encryptionSpec.AWS.BackupKey)
		if err != nil {
			return err
		}
		encryptionConfigurationBytes = awsKMSEncryptionConfigBytes
	default:
		return fmt.Errorf("unrecognized kms provider %s", encryptionSpec.Provider)
	}
	config.Data[secretEncryptionConfigurationKey] = encryptionConfigurationBytes
	return nil
}

func ReconcileAESCBCEncryptionConfig(config *corev1.Secret,
	ownerRef hcpconfig.OwnerRef,
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
