package kas

import (
	"bytes"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

func applyKMSConfig(podSpec *corev1.PodSpec, secretEncryptionData *hyperv1.SecretEncryptionSpec, images kmsImages) error {
	if secretEncryptionData.KMS == nil {
		return fmt.Errorf("kms metadata not specified")
	}

	provider, err := getKMSProvider(secretEncryptionData.KMS, images)
	if err != nil {
		return err
	}
	kmsPodConfig, err := provider.GenerateKMSPodConfig()
	if err != nil {
		return err
	}

	podSpec.Containers = append(podSpec.Containers, kmsPodConfig.Containers...)
	podSpec.Volumes = append(podSpec.Volumes, kmsPodConfig.Volumes...)
	util.UpdateContainer(ComponentName, podSpec.Containers, kmsPodConfig.KASContainerMutate)

	return nil
}

func generateKMSEncryptionConfig(kmsSpec *hyperv1.KMSSpec, apiVersion string) ([]byte, error) {
	provider, err := getKMSProvider(kmsSpec, kmsImages{})
	if err != nil {
		return nil, err
	}

	encryptionConfig, err := provider.GenerateKMSEncryptionConfig(apiVersion)
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

func getKMSProvider(kmsSpec *hyperv1.KMSSpec, images kmsImages) (kms.KMSProvider, error) {
	switch kmsSpec.Provider {
	case hyperv1.IBMCloud:
		return kms.NewIBMCloudKMSProvider(kmsSpec.IBMCloud, images.IBMCloudKMS)
	case hyperv1.AWS:
		return kms.NewAWSKMSProvider(kmsSpec.AWS, images.AWSKMS, images.TokenMinterImage)
	case hyperv1.AZURE:
		return kms.NewAzureKMSProvider(kmsSpec.Azure, images.AzureKMS)
	default:
		return nil, fmt.Errorf("unrecognized kms provider %s", kmsSpec.Provider)
	}
}
