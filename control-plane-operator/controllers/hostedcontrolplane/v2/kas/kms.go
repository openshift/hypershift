package kas

import (
	"bytes"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/podspec"

	corev1 "k8s.io/api/core/v1"
)

func applyKMSConfig(podSpec *corev1.PodSpec, secretEncryptionData *hyperv1.SecretEncryptionSpec, images kmsImages, hcp *hyperv1.HostedControlPlane) error {
	if secretEncryptionData.KMS == nil {
		return fmt.Errorf("kms metadata not specified")
	}

	provider, err := getKMSProvider(secretEncryptionData.KMS, images, hcp)
	if err != nil {
		return err
	}
	kmsPodConfig, err := provider.GenerateKMSPodConfig()
	if err != nil {
		return err
	}

	podSpec.Containers = append(podSpec.Containers, kmsPodConfig.Containers...)
	podSpec.Volumes = append(podSpec.Volumes, kmsPodConfig.Volumes...)
	podspec.UpdateContainer(ComponentName, podSpec.Containers, kmsPodConfig.KASContainerMutate)

	return nil
}

func generateKMSEncryptionConfig(kmsSpec *hyperv1.KMSSpec, apiVersion string) ([]byte, error) {
	provider, err := getKMSProvider(kmsSpec, kmsImages{}, nil)
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

// getKMSProvider returns a KMS provider for the given spec. When hcp is nil (called from
// generateKMSEncryptionConfig), the provider is always created as "managed" because encryption
// config generation only produces the EncryptionConfiguration resource and does not need
// platform-specific pod/volume configuration.
func getKMSProvider(kmsSpec *hyperv1.KMSSpec, images kmsImages, hcp *hyperv1.HostedControlPlane) (kms.KMSProvider, error) {
	switch kmsSpec.Provider {
	case hyperv1.IBMCloud:
		return kms.NewIBMCloudKMSProvider(kmsSpec.IBMCloud, images.IBMCloudKMS)
	case hyperv1.AWS:
		return kms.NewAWSKMSProvider(kmsSpec.AWS, images.AWSKMS, images.TokenMinterImage)
	case hyperv1.AZURE:
		isSelfManaged := hcp != nil && azureutil.IsSelfManagedAzure(hcp.Spec.Platform.Type)
		opts := kms.AzureKMSProviderOptions{
			IsSelfManaged:    isSelfManaged,
			TokenMinterImage: images.TokenMinterImage,
		}
		if isSelfManaged && kmsSpec.Azure.WorkloadIdentity.ClientID != "" {
			opts.KMSClientID = string(kmsSpec.Azure.WorkloadIdentity.ClientID)
			opts.TenantID = hcp.Spec.Platform.Azure.TenantID
		}
		return kms.NewAzureKMSProvider(kmsSpec.Azure, images.AzureKMS, opts)
	default:
		return nil, fmt.Errorf("unrecognized kms provider %s", kmsSpec.Provider)
	}
}
