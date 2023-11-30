package kas

import (
	"bytes"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas/kms"
	"github.com/openshift/hypershift/support/api"
	corev1 "k8s.io/api/core/v1"
)

func applyKMSConfig(podSpec *corev1.PodSpec, secretEncryptionData *hyperv1.SecretEncryptionSpec, images KubeAPIServerImages) error {
	if secretEncryptionData.KMS == nil {
		return fmt.Errorf("kms metadata not specified")
	}
	provider, err := GetKMSProvider(secretEncryptionData.KMS, images)
	if err != nil {
		return err
	}
	return provider.ApplyKMSConfig(podSpec)
}

func generateKMSEncryptionConfig(kmsSpec *hyperv1.KMSSpec) ([]byte, error) {
	provider, err := GetKMSProvider(kmsSpec, KubeAPIServerImages{})
	if err != nil {
		return nil, err
	}

	encryptionConfig, err := provider.GenerateKMSEncryptionConfig()
	if err != nil {
		return nil, err
	}

	bufferInstance := bytes.NewBuffer([]byte{})
	err = api.YamlSerializer.Encode(encryptionConfig, bufferInstance)
	if err != nil {
		return nil, err
	}
	return bufferInstance.Bytes(), nil
}

func GetKMSProvider(kmsSpec *hyperv1.KMSSpec, images KubeAPIServerImages) (kms.IKMSProvider, error) {
	switch kmsSpec.Provider {
	case hyperv1.IBMCloud:
		return kms.NewIBMCloudKMSProvider(kmsSpec.IBMCloud, images.IBMCloudKMS)
	case hyperv1.AWS:
		return kms.NewAWSKMSProvider(kmsSpec.AWS, images.AWSKMS, images.TokenMinterImage)
	case hyperv1.AZURE:
		return kms.NewAzureKMSProvider(kmsSpec.Azure)
	default:
		return nil, fmt.Errorf("unrecognized kms provider %s", kmsSpec.Provider)
	}
}
