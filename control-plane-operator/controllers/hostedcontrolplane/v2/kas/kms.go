package kas

import (
	"bytes"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/secretencryption"

	corev1 "k8s.io/api/core/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

func applyKMSConfig(podSpec *corev1.PodSpec, secretEncryptionData *hyperv1.SecretEncryptionSpec, encStatus *hyperv1.SecretEncryptionStatus, currentConfig *apiserverv1.EncryptionConfiguration, kasConverged bool, images kmsImages, hcp *hyperv1.HostedControlPlane) error {
	if secretEncryptionData.KMS == nil {
		return fmt.Errorf("kms metadata not specified")
	}

	keys, err := deriveKMSKeys(secretEncryptionData.KMS, encStatus, currentConfig, kasConverged)
	if err != nil {
		return fmt.Errorf("failed to derive KMS keys: %w", err)
	}
	provider, err := getKMSProvider(secretEncryptionData.KMS, keys, images, hcp)
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

func generateKMSEncryptionConfig(kmsSpec *hyperv1.KMSSpec, encStatus *hyperv1.SecretEncryptionStatus, currentConfig *apiserverv1.EncryptionConfiguration, kasConverged bool, apiVersion string) ([]byte, error) {
	keys, err := deriveKMSKeys(kmsSpec, encStatus, currentConfig, kasConverged)
	if err != nil {
		return nil, fmt.Errorf("failed to derive KMS keys: %w", err)
	}
	provider, err := getKMSProvider(kmsSpec, keys, kmsImages{}, nil)
	if err != nil {
		return nil, err
	}

	encryptionConfig, err := provider.GenerateKMSEncryptionConfig(apiVersion)
	if err != nil {
		return nil, err
	}

	bufferInstance := bytes.NewBuffer([]byte{})
	if err := api.YamlSerializer.Encode(encryptionConfig, bufferInstance); err != nil {
		return nil, err
	}
	return bufferInstance.Bytes(), nil
}

// kmsWriteReadKeys holds the provider-specific write and read key assignments
// derived from the HCP status and current EncryptionConfiguration.
type kmsWriteReadKeys struct {
	awsWrite   *hyperv1.AWSKMSKeyEntry
	awsRead    *hyperv1.AWSKMSKeyEntry
	azureWrite *hyperv1.AzureKMSKey
	azureRead  *hyperv1.AzureKMSKey
}

// deriveKMSKeys determines the write and read KMS keys using the two-stage
// rollout pattern. It inspects the current EncryptionConfiguration and KAS
// convergence to determine the correct stage:
//
//   - No targetKey: no rotation, spec.activeKey is the sole write key.
//   - targetKey set, not yet in config or present as read-only and KAS not
//     converged: ReadOnlyDeploy stage — old key (status.activeKey) writes,
//     new key (status.targetKey) reads.
//   - targetKey present as read-only and KAS converged, or targetKey is
//     already write provider: WritePromote/Migrating — new key
//     (status.targetKey) writes, old key (status.activeKey) reads.
//   - status has no active key (upgrade transition): fall back to spec.backupKey.
func deriveKMSKeys(kmsSpec *hyperv1.KMSSpec, encStatus *hyperv1.SecretEncryptionStatus, currentConfig *apiserverv1.EncryptionConfiguration, kasConverged bool) (kmsWriteReadKeys, error) {
	keys := kmsWriteReadKeys{}

	switch kmsSpec.Provider {
	case hyperv1.AWS:
		if kmsSpec.AWS == nil {
			return keys, nil
		}

		if encStatus == nil || encStatus.ActiveKey.Provider == "" {
			// Upgrade transition or initial setup: use spec keys with deprecated backupKey fallback.
			keys.awsWrite = &kmsSpec.AWS.ActiveKey
			if kmsSpec.AWS.BackupKey != nil { //nolint:staticcheck
				keys.awsRead = kmsSpec.AWS.BackupKey //nolint:staticcheck
			}
			return keys, nil
		}

		if encStatus.TargetKey.Provider == "" || encStatus.TargetKey.AWS.ARN == "" || encStatus.ActiveKey.AWS.ARN == "" {
			keys.awsWrite = &kmsSpec.AWS.ActiveKey
			return keys, nil
		}

		// Rotation in progress. Determine the stage from the current config.
		targetKey := &hyperv1.AWSKMSKeyEntry{ARN: encStatus.TargetKey.AWS.ARN}
		oldKey := &hyperv1.AWSKMSKeyEntry{ARN: encStatus.ActiveKey.AWS.ARN}
		targetName, err := kms.AWSKMSProviderName(targetKey.ARN)
		if err != nil {
			return keys, err
		}

		if secretencryption.ShouldPromoteTargetKey(currentConfig, targetName, hyperv1.KMS, kasConverged) {
			keys.awsWrite = targetKey
			keys.awsRead = oldKey
		} else {
			keys.awsWrite = oldKey
			keys.awsRead = targetKey
		}

	case hyperv1.AZURE:
		if kmsSpec.Azure == nil {
			return keys, nil
		}

		if encStatus == nil || encStatus.ActiveKey.Provider == "" {
			keys.azureWrite = &kmsSpec.Azure.ActiveKey
			if kmsSpec.Azure.BackupKey != nil { //nolint:staticcheck
				keys.azureRead = kmsSpec.Azure.BackupKey //nolint:staticcheck
			}
			return keys, nil
		}

		if encStatus.TargetKey.Provider == "" || encStatus.TargetKey.Azure.KeyVaultName == "" || encStatus.ActiveKey.Azure.KeyVaultName == "" {
			keys.azureWrite = &kmsSpec.Azure.ActiveKey
			return keys, nil
		}

		targetKey := &hyperv1.AzureKMSKey{
			KeyVaultName: encStatus.TargetKey.Azure.KeyVaultName,
			KeyName:      encStatus.TargetKey.Azure.KeyName,
			KeyVersion:   encStatus.TargetKey.Azure.KeyVersion,
		}
		oldKey := &hyperv1.AzureKMSKey{
			KeyVaultName: encStatus.ActiveKey.Azure.KeyVaultName,
			KeyName:      encStatus.ActiveKey.Azure.KeyName,
			KeyVersion:   encStatus.ActiveKey.Azure.KeyVersion,
		}
		targetName, err := kms.AzureKMSProviderName(*targetKey)
		if err != nil {
			return keys, err
		}

		if secretencryption.ShouldPromoteTargetKey(currentConfig, targetName, hyperv1.KMS, kasConverged) {
			keys.azureWrite = targetKey
			keys.azureRead = oldKey
		} else {
			keys.azureWrite = oldKey
			keys.azureRead = targetKey
		}

	case hyperv1.IBMCloud:
		// IBM Cloud uses a single KMS provider with a fixed name; the sidecar
		// handles key versioning internally via KP_DATA_JSON. No write/read
		// key derivation is needed.
	}

	return keys, nil
}

// getKMSProvider returns a KMS provider for the given spec. When hcp is nil (called from
// generateKMSEncryptionConfig), the provider is always created as "managed" because encryption
// config generation only produces the EncryptionConfiguration resource and does not need
// platform-specific pod/volume configuration.
func getKMSProvider(kmsSpec *hyperv1.KMSSpec, keys kmsWriteReadKeys, images kmsImages, hcp *hyperv1.HostedControlPlane) (kms.KMSProvider, error) {
	switch kmsSpec.Provider {
	case hyperv1.IBMCloud:
		return kms.NewIBMCloudKMSProvider(kmsSpec.IBMCloud, images.IBMCloudKMS)
	case hyperv1.AWS:
		if kmsSpec.AWS == nil {
			return nil, fmt.Errorf("AWS kms metadata not specified")
		}
		return kms.NewAWSKMSProvider(*keys.awsWrite, keys.awsRead, kmsSpec.AWS.Region, images.AWSKMS, images.TokenMinterImage)
	case hyperv1.AZURE:
		if kmsSpec.Azure == nil {
			return nil, fmt.Errorf("azure kms metadata not specified")
		}
		opts := kms.AzureKMSProviderOptions{
			TokenMinterImage: images.TokenMinterImage,
		}
		if hcp != nil {
			opts.IsSelfManaged = azureutil.IsSelfManagedAzure(hcp.Spec.Platform.Type)
			if opts.IsSelfManaged && kmsSpec.Azure.WorkloadIdentity.ClientID != "" {
				opts.KMSClientID = string(kmsSpec.Azure.WorkloadIdentity.ClientID)
				opts.TenantID = hcp.Spec.Platform.Azure.TenantID
			}
		}
		return kms.NewAzureKMSProvider(*keys.azureWrite, keys.azureRead, kmsSpec.Azure, images.AzureKMS, opts)
	default:
		return nil, fmt.Errorf("unrecognized kms provider %s", kmsSpec.Provider)
	}
}
