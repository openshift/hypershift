package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/secretencryption"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	secretEncryptionConfigurationKey = secretencryption.EncryptionConfigurationKey
	encryptionConfigurationKind      = secretencryption.EncryptionConfigurationKind

	secretEncryptionConfigFileVolumeName = "kas-secret-encryption-config"
)

func secretEncryptionConfigPredicate(cpContext component.WorkloadContext) bool {
	return cpContext.HCP.Spec.SecretEncryption != nil
}

func adaptSecretEncryptionConfig(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	var data []byte
	secretEncryption := cpContext.HCP.Spec.SecretEncryption
	encStatus := &cpContext.HCP.Status.SecretEncryption

	// Read the live encryption config from the cluster to derive the two-stage rollout state.
	currentConfig, err := readCurrentEncryptionConfig(cpContext, secret)
	if err != nil {
		return fmt.Errorf("failed to read current encryption config: %w", err)
	}

	// Check KAS convergence — needed to decide whether to promote the target key.
	kasConverged, err := isKASConverged(cpContext)
	if err != nil {
		return fmt.Errorf("failed to check KAS convergence: %w", err)
	}

	switch secretEncryption.Type {
	case hyperv1.AESCBC:
		data, err = deriveAESCBCEncryptionConfig(cpContext, secretEncryption, encStatus, currentConfig, kasConverged)
		if err != nil {
			return err
		}
	case hyperv1.KMS:
		if secretEncryption.KMS == nil {
			return fmt.Errorf("kms metadata not specified")
		}
		apiVersion := getKMSAPIVersion(currentConfig)
		data, err = generateKMSEncryptionConfig(secretEncryption.KMS, encStatus, currentConfig, kasConverged, apiVersion)
		if err != nil {
			return err
		}
	}

	secret.Data[secretEncryptionConfigurationKey] = data
	return nil
}

// isKASConverged checks if the KAS Deployment has fully rolled out.
// Returns false (not error) if the deployment doesn't exist yet.
func isKASConverged(cpContext component.WorkloadContext) (bool, error) {
	kasDeployment := &appsv1.Deployment{}
	kasRef := manifests.KASDeployment(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(kasRef), kasDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get KAS deployment: %w", err)
	}
	return podspec.IsDeploymentReady(cpContext, kasDeployment), nil
}

// readCurrentEncryptionConfig reads the live encryption config secret from the
// cluster and parses its EncryptionConfiguration. Returns nil (not an error)
// if the secret does not exist yet.
func readCurrentEncryptionConfig(cpContext component.WorkloadContext, templateSecret *corev1.Secret) (*apiserverv1.EncryptionConfiguration, error) {
	existingSecret := &corev1.Secret{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(templateSecret), existingSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	configBytes := existingSecret.Data[secretEncryptionConfigurationKey]
	if len(configBytes) == 0 {
		return nil, nil
	}

	return secretencryption.DecodeEncryptionConfiguration(configBytes)
}

// deriveAESCBCEncryptionConfig determines the AESCBC write and read keys using
// the two-stage rollout pattern, then generates the EncryptionConfiguration.
//
// Two-stage derivation:
//   - No targetKey: spec.activeKey is the sole write key.
//   - targetKey set, target not yet promoted in current config: ReadOnlyDeploy —
//     old key (status.activeKey) writes, new key (status.targetKey) reads.
//   - targetKey set and already promoted in current config: WritePromote/Migrating —
//     new key (status.targetKey) writes, old key (status.activeKey) reads.
//   - status has no active key (upgrade transition): fall back to spec.backupKey.
func deriveAESCBCEncryptionConfig(cpContext component.WorkloadContext, secretEncryption *hyperv1.SecretEncryptionSpec, encStatus *hyperv1.SecretEncryptionStatus, currentConfig *apiserverv1.EncryptionConfiguration, kasConverged bool) ([]byte, error) {
	if secretEncryption.AESCBC == nil || len(secretEncryption.AESCBC.ActiveKey.Name) == 0 {
		return nil, fmt.Errorf("aescbc metadata not specified")
	}

	if encStatus == nil || encStatus.ActiveKey.Provider == "" {
		// Upgrade transition or initial setup: use spec keys with deprecated backupKey fallback.
		writeKeyData, err := fetchAESCBCKeyData(cpContext, secretEncryption.AESCBC.ActiveKey.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get aescbc active key: %w", err)
		}
		var readKeyData []byte
		if secretEncryption.AESCBC.BackupKey != nil && len(secretEncryption.AESCBC.BackupKey.Name) > 0 { //nolint:staticcheck
			readKeyData, err = fetchAESCBCKeyData(cpContext, secretEncryption.AESCBC.BackupKey.Name) //nolint:staticcheck
			if err != nil {
				return nil, fmt.Errorf("failed to get aescbc backup key: %w", err)
			}
		}
		return generateAESCBCEncryptionConfig(writeKeyData, readKeyData)
	}

	if encStatus.TargetKey.Provider == "" || encStatus.TargetKey.AESCBC.DataHash == "" || encStatus.ActiveKey.AESCBC.DataHash == "" {
		// No rotation in progress or provider mismatch: spec.activeKey is the sole write key.
		writeKeyData, err := fetchAESCBCKeyData(cpContext, secretEncryption.AESCBC.ActiveKey.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get aescbc active key: %w", err)
		}
		return generateAESCBCEncryptionConfig(writeKeyData, nil)
	}

	// Rotation in progress. Fetch both keys.
	targetSecretName := encStatus.TargetKey.AESCBC.Secret.Name
	oldSecretName := encStatus.ActiveKey.AESCBC.Secret.Name
	targetKeyData, err := fetchAESCBCKeyData(cpContext, targetSecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get aescbc target key secret %q: %w", targetSecretName, err)
	}
	oldKeyData, err := fetchAESCBCKeyData(cpContext, oldSecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get aescbc old key secret %q: %w", oldSecretName, err)
	}

	// Determine stage from current EncryptionConfiguration.
	targetKeyName, err := AESCBCKeyName(targetKeyData)
	if err != nil {
		return nil, fmt.Errorf("failed to compute aescbc target key name: %w", err)
	}
	if secretencryption.ShouldPromoteTargetKey(currentConfig, targetKeyName, hyperv1.AESCBC, kasConverged) {
		return generateAESCBCEncryptionConfig(targetKeyData, oldKeyData)
	}
	// ReadOnlyDeploy: old key writes, target key reads.
	return generateAESCBCEncryptionConfig(oldKeyData, targetKeyData)
}

// fetchAESCBCKeyData retrieves the AESCBC key data from a named secret.
func fetchAESCBCKeyData(cpContext component.WorkloadContext, secretName string) ([]byte, error) {
	keySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cpContext.HCP.Namespace,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(keySecret), keySecret); err != nil {
		return nil, err
	}
	keyData, ok := keySecret.Data[hyperv1.AESCBCKeySecretKey]
	if !ok {
		return nil, fmt.Errorf("aescbc key field %q not found in secret %q", hyperv1.AESCBCKeySecretKey, secretName)
	}
	return keyData, nil
}

// getKMSAPIVersion extracts the KMS API version from the current EncryptionConfiguration.
// Returns "v2" as default if no config exists or no KMS provider is configured.
func getKMSAPIVersion(currentConfig *apiserverv1.EncryptionConfiguration) string {
	if currentConfig != nil {
		for _, r := range currentConfig.Resources {
			if len(r.Providers) > 0 && r.Providers[0].KMS != nil {
				return r.Providers[0].KMS.APIVersion
			}
		}
	}
	return "v2"
}

func buildVolumeSecretEncryptionConfigFile() corev1.Volume {
	v := corev1.Volume{
		Name: secretEncryptionConfigFileVolumeName,
	}
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASSecretEncryptionConfigFile("").Name
	return v
}
