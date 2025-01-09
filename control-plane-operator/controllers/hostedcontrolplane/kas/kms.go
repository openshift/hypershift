package kas

import (
	"bytes"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas/kms"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
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

// getKMSAPIVersion returns the KMS API version from the given EncryptionConfig
// secret.  If the current state is using the IdentityProvider, the function
// returns v2 as the default version to start with.
func getKMSAPIVersion(config *corev1.Secret) (string, error) {
	apiVersion := "v2"
	encryptionConfigBytes := config.Data[secretEncryptionConfigurationKey]
	if len(encryptionConfigBytes) > 0 {
		currentConfig := v1.EncryptionConfiguration{}
		gvks, _, err := api.Scheme.ObjectKinds(&currentConfig)
		if err != nil || len(gvks) == 0 {
			return "", fmt.Errorf("cannot determine gvk of resource: %v", err)
		}
		if _, _, err = api.YamlSerializer.Decode(encryptionConfigBytes, &gvks[0], &currentConfig); err != nil {
			return "", fmt.Errorf("cannot decode resource: %v", err)
		}

		// Only look at write keys to return the APIVersion currently used.
		for _, r := range currentConfig.Resources {
			if len(r.Providers) > 0 && r.Providers[0].KMS != nil {
				return r.Providers[0].KMS.APIVersion, nil
			}
		}
	}
	return apiVersion, nil
}

func generateKMSEncryptionConfig(config *corev1.Secret, kmsSpec *hyperv1.KMSSpec) ([]byte, error) {
	provider, err := GetKMSProvider(kmsSpec, KubeAPIServerImages{})
	if err != nil {
		return nil, err
	}

	apiVersion, err := getKMSAPIVersion(config)
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

func GetKMSProvider(kmsSpec *hyperv1.KMSSpec, images KubeAPIServerImages) (kms.IKMSProvider, error) {
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
