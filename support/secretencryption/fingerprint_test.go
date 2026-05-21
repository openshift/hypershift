package secretencryption

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestFingerprintAzureKMSKey(t *testing.T) {
	t.Parallel()
	t.Run("When computing Azure KMS fingerprint it should be deterministic", func(t *testing.T) {
		g := NewWithT(t)
		key := hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v1"}
		fp1 := FingerprintAzureKMSKey(key)
		fp2 := FingerprintAzureKMSKey(key)
		g.Expect(fp1).To(Equal(fp2))
		g.Expect(fp1).ToNot(BeEmpty())
	})

	t.Run("When Azure KMS key version changes it should produce a different fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		key1 := hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v1"}
		key2 := hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v2"}
		g.Expect(FingerprintAzureKMSKey(key1)).ToNot(Equal(FingerprintAzureKMSKey(key2)))
	})
}

func TestFingerprintAWSKMSKey(t *testing.T) {
	t.Parallel()
	t.Run("When computing AWS KMS fingerprint it should hash the ARN", func(t *testing.T) {
		g := NewWithT(t)
		fp := FingerprintAWSKMSKey("arn:aws:kms:us-east-1:123456789:key/test-key")
		g.Expect(fp).ToNot(BeEmpty())
		g.Expect(fp).To(Equal(FingerprintAWSKMSKey("arn:aws:kms:us-east-1:123456789:key/test-key")))
	})

	t.Run("When AWS KMS ARN changes it should produce a different fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		fp1 := FingerprintAWSKMSKey("arn:aws:kms:us-east-1:123456789:key/key-1")
		fp2 := FingerprintAWSKMSKey("arn:aws:kms:us-east-1:123456789:key/key-2")
		g.Expect(fp1).ToNot(Equal(fp2))
	})
}

func TestFingerprintIBMCloudKMSKeyList(t *testing.T) {
	t.Parallel()
	t.Run("When computing IBM Cloud fingerprint it should use CRK ID, instance ID, and key version", func(t *testing.T) {
		g := NewWithT(t)
		entries := []hyperv1.IBMCloudKMSKeyEntry{
			{CRKID: "crk1", InstanceID: "inst1", KeyVersion: 1, CorrelationID: "corr1", URL: "https://kms.example.com"},
		}
		fp := FingerprintIBMCloudKMSKeyList(entries)
		g.Expect(fp).ToNot(BeEmpty())
	})

	t.Run("When IBM Cloud metadata changes but identity stays same it should produce same fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		entries1 := []hyperv1.IBMCloudKMSKeyEntry{
			{CRKID: "crk1", InstanceID: "inst1", KeyVersion: 1, CorrelationID: "corr1", URL: "https://url1.com"},
		}
		entries2 := []hyperv1.IBMCloudKMSKeyEntry{
			{CRKID: "crk1", InstanceID: "inst1", KeyVersion: 1, CorrelationID: "corr2", URL: "https://url2.com"},
		}
		g.Expect(FingerprintIBMCloudKMSKeyList(entries1)).To(Equal(FingerprintIBMCloudKMSKeyList(entries2)))
	})

	t.Run("When IBM Cloud key version changes it should produce a different fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		entries1 := []hyperv1.IBMCloudKMSKeyEntry{
			{CRKID: "crk1", InstanceID: "inst1", KeyVersion: 1},
		}
		entries2 := []hyperv1.IBMCloudKMSKeyEntry{
			{CRKID: "crk1", InstanceID: "inst1", KeyVersion: 2},
		}
		g.Expect(FingerprintIBMCloudKMSKeyList(entries1)).ToNot(Equal(FingerprintIBMCloudKMSKeyList(entries2)))
	})
}

func TestFingerprintAESCBCKey(t *testing.T) {
	t.Parallel()
	t.Run("When computing AESCBC fingerprint it should combine secret name and data hash", func(t *testing.T) {
		g := NewWithT(t)
		fp := FingerprintAESCBCKey("my-secret", "abc123")
		g.Expect(fp).ToNot(BeEmpty())
		g.Expect(fp).To(Equal(FingerprintAESCBCKey("my-secret", "abc123")))
	})

	t.Run("When AESCBC secret name changes it should produce a different fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		fp1 := FingerprintAESCBCKey("secret-1", "abc123")
		fp2 := FingerprintAESCBCKey("secret-2", "abc123")
		g.Expect(fp1).ToNot(Equal(fp2))
	})

	t.Run("When AESCBC data hash changes it should produce a different fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		fp1 := FingerprintAESCBCKey("my-secret", "hash1")
		fp2 := FingerprintAESCBCKey("my-secret", "hash2")
		g.Expect(fp1).ToNot(Equal(fp2))
	})
}

func TestFingerprintFromKeyStatus(t *testing.T) {
	t.Parallel()
	t.Run("When status is nil it should return empty string", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(FingerprintFromKeyStatus(nil)).To(BeEmpty())
	})

	t.Run("When Azure status is provided it should match spec fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		key := hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v1"}
		status := &hyperv1.SecretEncryptionKeyStatus{
			Provider: hyperv1.SecretEncryptionProviderAzure,
			Azure:    hyperv1.AzureKMSKeyStatus{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v1"},
		}
		g.Expect(FingerprintFromKeyStatus(status)).To(Equal(FingerprintAzureKMSKey(key)))
	})

	t.Run("When AWS status is provided it should match spec fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		arn := "arn:aws:kms:us-east-1:123:key/test"
		status := &hyperv1.SecretEncryptionKeyStatus{
			Provider: hyperv1.SecretEncryptionProviderAWS,
			AWS:      hyperv1.AWSKMSKeyStatus{ARN: arn, Region: "us-east-1"},
		}
		g.Expect(FingerprintFromKeyStatus(status)).To(Equal(FingerprintAWSKMSKey(arn)))
	})
}
