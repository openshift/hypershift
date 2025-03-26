package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	secretEncryptionConfigurationKey = "config.yaml"
	encryptionConfigurationKind      = "EncryptionConfiguration"

	secretEncryptionConfigFileVolumeName = "kas-secret-encryption-config"
)

func secretEncryptionConfigPredicate(cpContext component.WorkloadContext) bool {
	return cpContext.HCP.Spec.SecretEncryption != nil
}

func adaptSecretEncryptionConfig(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	var data []byte
	secretEncryption := cpContext.HCP.Spec.SecretEncryption
	switch secretEncryption.Type {
	case hyperv1.AESCBC:
		if secretEncryption.AESCBC == nil || len(secretEncryption.AESCBC.ActiveKey.Name) == 0 {
			return fmt.Errorf("aescbc metadata not specified")
		}
		activeKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretEncryption.AESCBC.ActiveKey.Name,
				Namespace: cpContext.HCP.Namespace,
			},
		}
		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(activeKeySecret), activeKeySecret); err != nil {
			return fmt.Errorf("failed to get aescbc active secret: %w", err)
		}
		if _, ok := activeKeySecret.Data[hyperv1.AESCBCKeySecretKey]; !ok {
			return fmt.Errorf("aescbc key field '%s' in active key secret not specified", hyperv1.AESCBCKeySecretKey)
		}
		aesCBCActiveKey := activeKeySecret.Data[hyperv1.AESCBCKeySecretKey]
		var aesCBCBackupKey []byte
		if secretEncryption.AESCBC.BackupKey != nil && len(secretEncryption.AESCBC.BackupKey.Name) > 0 {
			backupKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretEncryption.AESCBC.BackupKey.Name,
					Namespace: cpContext.HCP.Namespace,
				},
			}
			if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(backupKeySecret), backupKeySecret); err != nil {
				return fmt.Errorf("failed to get aescbc backup key secret: %w", err)
			}
			if _, ok := backupKeySecret.Data[hyperv1.AESCBCKeySecretKey]; !ok {
				return fmt.Errorf("aescbc key field %s in backup key secret not specified", hyperv1.AESCBCKeySecretKey)
			}
			aesCBCBackupKey = backupKeySecret.Data[hyperv1.AESCBCKeySecretKey]
		}

		var err error
		data, err = generateAESCBCEncryptionConfig(aesCBCActiveKey, aesCBCBackupKey)
		if err != nil {
			return err
		}
	case hyperv1.KMS:
		if secretEncryption.KMS == nil {
			return fmt.Errorf("kms metadata not specified")
		}
		apiVersion, err := getKMSAPIVersion(cpContext, secret)
		if err != nil {
			return err
		}
		data, err = generateKMSEncryptionConfig(secretEncryption.KMS, apiVersion)
		if err != nil {
			return err
		}
	}

	secret.Data[secretEncryptionConfigurationKey] = data
	return nil
}

// getKMSAPIVersion returns the KMS API version from the given EncryptionConfig secret.
// If the current state is using the IdentityProvider, the function returns v2 as the default version to start with.
func getKMSAPIVersion(cpContext component.WorkloadContext, secret *corev1.Secret) (string, error) {
	apiVersion := "v2"
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(secret), secret); err != nil {
		if apierrors.IsNotFound(err) {
			return apiVersion, nil
		}
		return "", fmt.Errorf("failed to get existing secret encryption config: %v", err)
	}

	encryptionConfigBytes := secret.Data[secretEncryptionConfigurationKey]
	if len(encryptionConfigBytes) > 0 {
		currentConfig := apiserverv1.EncryptionConfiguration{}
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

func buildVolumeSecretEncryptionConfigFile() corev1.Volume {
	v := corev1.Volume{
		Name: secretEncryptionConfigFileVolumeName,
	}
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASSecretEncryptionConfigFile("").Name
	return v
}
