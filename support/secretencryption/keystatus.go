package secretencryption

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

// KeyStatusFromAzureSpec creates a SecretEncryptionKeyStatus from an Azure KMS key spec.
func KeyStatusFromAzureSpec(key hyperv1.AzureKMSKey) *hyperv1.SecretEncryptionKeyStatus {
	return &hyperv1.SecretEncryptionKeyStatus{
		Provider: hyperv1.SecretEncryptionProviderAzure,
		Azure:    key,
	}
}

// KeyStatusFromAWSSpec creates a SecretEncryptionKeyStatus from an AWS KMS key spec.
func KeyStatusFromAWSSpec(key hyperv1.AWSKMSKeyEntry) *hyperv1.SecretEncryptionKeyStatus {
	return &hyperv1.SecretEncryptionKeyStatus{
		Provider: hyperv1.SecretEncryptionProviderAWS,
		AWS:      key,
	}
}

// KeyStatusFromIBMCloudSpec creates a SecretEncryptionKeyStatus from IBM Cloud KMS key entries.
// Uses the first entry in the key list as the representative key.
func KeyStatusFromIBMCloudSpec(entries []hyperv1.IBMCloudKMSKeyEntry) *hyperv1.SecretEncryptionKeyStatus {
	if len(entries) == 0 {
		return nil
	}
	return &hyperv1.SecretEncryptionKeyStatus{
		Provider: hyperv1.SecretEncryptionProviderIBMCloud,
		IBMCloud: entries[0],
	}
}

// KeyStatusFromAESCBCSpec creates a SecretEncryptionKeyStatus from an AESCBC spec.
func KeyStatusFromAESCBCSpec(secretRef corev1.LocalObjectReference, dataHash string) *hyperv1.SecretEncryptionKeyStatus {
	return &hyperv1.SecretEncryptionKeyStatus{
		Provider: hyperv1.SecretEncryptionProviderAESCBC,
		AESCBC: hyperv1.AESCBCKeyStatus{
			Secret:   secretRef,
			DataHash: dataHash,
		},
	}
}

// KeyStatusFromSpec creates a SecretEncryptionKeyStatus from a SecretEncryptionSpec.
// For AESCBC, the caller must provide the dataHash (SHA-256 of the secret's "key" data field).
func KeyStatusFromSpec(spec *hyperv1.SecretEncryptionSpec, aescbcDataHash string) *hyperv1.SecretEncryptionKeyStatus {
	if spec == nil {
		return nil
	}
	switch spec.Type {
	case hyperv1.KMS:
		if spec.KMS == nil {
			return nil
		}
		switch spec.KMS.Provider {
		case hyperv1.AZURE:
			if spec.KMS.Azure == nil {
				return nil
			}
			return KeyStatusFromAzureSpec(spec.KMS.Azure.ActiveKey)
		case hyperv1.AWS:
			if spec.KMS.AWS == nil {
				return nil
			}
			return KeyStatusFromAWSSpec(spec.KMS.AWS.ActiveKey)
		case hyperv1.IBMCloud:
			if spec.KMS.IBMCloud == nil {
				return nil
			}
			return KeyStatusFromIBMCloudSpec(spec.KMS.IBMCloud.KeyList)
		}
	case hyperv1.AESCBC:
		if spec.AESCBC == nil {
			return nil
		}
		return KeyStatusFromAESCBCSpec(spec.AESCBC.ActiveKey, aescbcDataHash)
	}
	return nil
}

// KeyReferenceFromStatus extracts an EncryptionKeyReference from a SecretEncryptionKeyStatus.
func KeyReferenceFromStatus(status *hyperv1.SecretEncryptionKeyStatus) hyperv1.EncryptionKeyReference {
	if status == nil {
		return hyperv1.EncryptionKeyReference{}
	}
	fp := FingerprintFromKeyStatus(status)
	return hyperv1.EncryptionKeyReference{
		Provider:    status.Provider,
		Fingerprint: fp,
	}
}
