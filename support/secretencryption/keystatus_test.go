package secretencryption

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

func TestKeyStatusFromAzureSpec(t *testing.T) {
	t.Parallel()
	t.Run("When creating key status from Azure spec it should populate all fields", func(t *testing.T) {
		g := NewWithT(t)
		key := hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v1"}
		status := KeyStatusFromAzureSpec(key)
		g.Expect(status.Provider).To(Equal(hyperv1.SecretEncryptionProviderAzure))
		g.Expect(status.Azure).ToNot(BeNil())
		g.Expect(status.Azure.KeyVaultName).To(Equal("vault"))
		g.Expect(status.Azure.KeyName).To(Equal("key"))
		g.Expect(status.Azure.KeyVersion).To(Equal("v1"))
	})
}

func TestKeyStatusFromSpec(t *testing.T) {
	t.Parallel()
	t.Run("When spec is nil it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(KeyStatusFromSpec(nil, "")).To(BeNil())
	})

	t.Run("When spec is Azure KMS it should create Azure key status", func(t *testing.T) {
		g := NewWithT(t)
		spec := &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					ActiveKey: hyperv1.AzureKMSKey{KeyVaultName: "v", KeyName: "k", KeyVersion: "1"},
				},
			},
		}
		status := KeyStatusFromSpec(spec, "")
		g.Expect(status).ToNot(BeNil())
		g.Expect(status.Provider).To(Equal(hyperv1.SecretEncryptionProviderAzure))
	})

	t.Run("When spec is AWS KMS it should create AWS key status", func(t *testing.T) {
		g := NewWithT(t)
		spec := &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123:key/test"},
					Region:    "us-east-1",
				},
			},
		}
		status := KeyStatusFromSpec(spec, "")
		g.Expect(status).ToNot(BeNil())
		g.Expect(status.Provider).To(Equal(hyperv1.SecretEncryptionProviderAWS))
		g.Expect(status.AWS.ARN).To(Equal("arn:aws:kms:us-east-1:123:key/test"))
	})

	t.Run("When spec is IBMCloud KMS it should create IBMCloud key status from first entry", func(t *testing.T) {
		g := NewWithT(t)
		spec := &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.IBMCloud,
				IBMCloud: &hyperv1.IBMCloudKMSSpec{
					KeyList: []hyperv1.IBMCloudKMSKeyEntry{
						{CRKID: "crk1", InstanceID: "inst1", KeyVersion: 1, CorrelationID: "corr1", URL: "https://kms.example.com"},
						{CRKID: "crk2", InstanceID: "inst2", KeyVersion: 2, CorrelationID: "corr2", URL: "https://kms.example.com"},
					},
				},
			},
		}
		status := KeyStatusFromSpec(spec, "")
		g.Expect(status).ToNot(BeNil())
		g.Expect(status.Provider).To(Equal(hyperv1.SecretEncryptionProviderIBMCloud))
		g.Expect(status.IBMCloud.CRKID).To(Equal("crk1"))
	})

	t.Run("When spec is IBMCloud KMS with empty key list it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		spec := &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.IBMCloud,
				IBMCloud: &hyperv1.IBMCloudKMSSpec{
					KeyList: []hyperv1.IBMCloudKMSKeyEntry{},
				},
			},
		}
		g.Expect(KeyStatusFromSpec(spec, "")).To(BeNil())
	})

	t.Run("When spec is AESCBC it should create AESCBC key status with data hash", func(t *testing.T) {
		g := NewWithT(t)
		spec := &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.AESCBC,
			AESCBC: &hyperv1.AESCBCSpec{
				ActiveKey: corev1.LocalObjectReference{Name: "my-secret"},
			},
		}
		status := KeyStatusFromSpec(spec, "deadbeef")
		g.Expect(status).ToNot(BeNil())
		g.Expect(status.Provider).To(Equal(hyperv1.SecretEncryptionProviderAESCBC))
		g.Expect(status.AESCBC.Secret.Name).To(Equal("my-secret"))
		g.Expect(status.AESCBC.DataHash).To(Equal("deadbeef"))
	})
}

func TestKeyReferenceFromStatus(t *testing.T) {
	t.Parallel()
	t.Run("When status is nil it should return empty reference", func(t *testing.T) {
		g := NewWithT(t)
		ref := KeyReferenceFromStatus(nil)
		g.Expect(ref.Provider).To(BeEmpty())
		g.Expect(ref.Fingerprint).To(BeEmpty())
	})

	t.Run("When status is Azure it should return correct provider and fingerprint", func(t *testing.T) {
		g := NewWithT(t)
		status := &hyperv1.SecretEncryptionKeyStatus{
			Provider: hyperv1.SecretEncryptionProviderAzure,
			Azure:    hyperv1.AzureKMSKey{KeyVaultName: "v", KeyName: "k", KeyVersion: "1"},
		}
		ref := KeyReferenceFromStatus(status)
		g.Expect(ref.Provider).To(Equal(hyperv1.SecretEncryptionProviderAzure))
		g.Expect(ref.Fingerprint).ToNot(BeEmpty())
	})
}
