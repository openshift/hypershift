package secretencryption

import (
	"crypto/sha256"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// DataHash computes the hex-encoded SHA-256 hash of raw data.
func DataHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// FingerprintAzureKMSKey computes the SHA-256 fingerprint for an Azure KMS key.
func FingerprintAzureKMSKey(key hyperv1.AzureKMSKey) string {
	return DataHash([]byte(key.KeyVaultName + "/" + key.KeyName + "/" + key.KeyVersion))
}

// FingerprintAWSKMSKey computes the SHA-256 fingerprint for an AWS KMS key.
func FingerprintAWSKMSKey(arn string) string {
	return DataHash([]byte(arn))
}

// FingerprintIBMCloudKMSKeyList computes the SHA-256 fingerprint for an IBM Cloud KMS key list.
// Only identity-relevant fields are included; CorrelationID and URL are excluded.
func FingerprintIBMCloudKMSKeyList(entries []hyperv1.IBMCloudKMSKeyEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.CRKID + "/" + e.InstanceID + "/" + fmt.Sprintf("%d", e.KeyVersion)
	}
	return DataHash([]byte(strings.Join(parts, ";")))
}

// FingerprintAESCBCKey computes the SHA-256 fingerprint for an AESCBC key.
// The dataHash parameter should be the hex-encoded SHA-256 of the secret's "key" data field.
func FingerprintAESCBCKey(secretName string, dataHash string) string {
	return DataHash([]byte(secretName + "/" + dataHash))
}

// FingerprintFromKeyStatus computes the fingerprint from a SecretEncryptionKeyStatus.
func FingerprintFromKeyStatus(status *hyperv1.SecretEncryptionKeyStatus) string {
	if status == nil {
		return ""
	}
	switch status.Provider {
	case hyperv1.SecretEncryptionProviderAzure:
		if status.Azure.KeyVaultName == "" {
			return ""
		}
		return FingerprintAzureKMSKey(hyperv1.AzureKMSKey{
			KeyVaultName: status.Azure.KeyVaultName,
			KeyName:      status.Azure.KeyName,
			KeyVersion:   status.Azure.KeyVersion,
		})
	case hyperv1.SecretEncryptionProviderAWS:
		if status.AWS.ARN == "" {
			return ""
		}
		return FingerprintAWSKMSKey(status.AWS.ARN)
	case hyperv1.SecretEncryptionProviderIBMCloud:
		if status.IBMCloud.CRKID == "" {
			return ""
		}
		return FingerprintIBMCloudKMSKeyList([]hyperv1.IBMCloudKMSKeyEntry{
			{
				CRKID:      status.IBMCloud.CRKID,
				InstanceID: status.IBMCloud.InstanceID,
				KeyVersion: int(status.IBMCloud.KeyVersion),
			},
		})
	case hyperv1.SecretEncryptionProviderAESCBC:
		if status.AESCBC.DataHash == "" {
			return ""
		}
		return FingerprintAESCBCKey(status.AESCBC.Secret.Name, status.AESCBC.DataHash)
	default:
		return ""
	}
}
