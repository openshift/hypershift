package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"

	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

func TestDeriveKMSKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		kmsSpec       *hyperv1.KMSSpec
		encStatus     *hyperv1.SecretEncryptionStatus
		currentConfig *apiserverv1.EncryptionConfiguration
		kasConverged  bool
		validate      func(g Gomega, keys kmsWriteReadKeys)
	}{
		{
			name: "When AWS with nil status it should use spec active key as write with spec backup as read",
			kmsSpec: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/new-key"},
					BackupKey: &hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/old-key"},
					Region:    "us-east-1",
				},
			},
			encStatus: nil,
			validate: func(g Gomega, keys kmsWriteReadKeys) {
				g.Expect(keys.awsWrite).ToNot(BeNil())
				g.Expect(keys.awsWrite.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/new-key"))
				g.Expect(keys.awsRead).ToNot(BeNil())
				g.Expect(keys.awsRead.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/old-key"))
			},
		},
		{
			name: "When AWS with status set but no target key it should use only write key",
			kmsSpec: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/current-key"},
					Region:    "us-east-1",
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAWS,
					AWS:      hyperv1.AWSKMSKeyStatus{ARN: "arn:aws:kms:us-east-1:123456789:key/current-key", Region: "us-east-1"},
				},
			},
			validate: func(g Gomega, keys kmsWriteReadKeys) {
				g.Expect(keys.awsWrite).ToNot(BeNil())
				g.Expect(keys.awsWrite.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/current-key"))
				g.Expect(keys.awsRead).To(BeNil())
			},
		},
		{
			name: "When AWS rotation in progress and target key absent from config it should use old key as write (ReadOnlyDeploy)",
			kmsSpec: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/new-key"},
					Region:    "us-east-1",
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAWS,
					AWS:      hyperv1.AWSKMSKeyStatus{ARN: "arn:aws:kms:us-east-1:123456789:key/old-key", Region: "us-east-1"},
				},
				TargetKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAWS,
					AWS:      hyperv1.AWSKMSKeyStatus{ARN: "arn:aws:kms:us-east-1:123456789:key/new-key", Region: "us-east-1"},
				},
			},
			currentConfig: nil,
			validate: func(g Gomega, keys kmsWriteReadKeys) {
				g.Expect(keys.awsWrite.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/old-key"), "old key should be write during ReadOnlyDeploy")
				g.Expect(keys.awsRead.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/new-key"), "new key should be read-only during ReadOnlyDeploy")
			},
		},
		{
			name: "When AWS rotation in progress and target key present in config it should promote target to write (WritePromote)",
			kmsSpec: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/new-key"},
					Region:    "us-east-1",
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAWS,
					AWS:      hyperv1.AWSKMSKeyStatus{ARN: "arn:aws:kms:us-east-1:123456789:key/old-key", Region: "us-east-1"},
				},
				TargetKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAWS,
					AWS:      hyperv1.AWSKMSKeyStatus{ARN: "arn:aws:kms:us-east-1:123456789:key/new-key", Region: "us-east-1"},
				},
			},
			currentConfig: kmsEncryptionConfig(
				mustAWSProviderName("arn:aws:kms:us-east-1:123456789:key/old-key"),
				mustAWSProviderName("arn:aws:kms:us-east-1:123456789:key/new-key"),
			),
			kasConverged: true,
			validate: func(g Gomega, keys kmsWriteReadKeys) {
				g.Expect(keys.awsWrite.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/new-key"), "target key should be write after promotion")
				g.Expect(keys.awsRead.ARN).To(Equal("arn:aws:kms:us-east-1:123456789:key/old-key"), "old key should be read after promotion")
			},
		},
		{
			name: "When Azure rotation in progress and target key absent from config it should use old key as write",
			kmsSpec: &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					ActiveKey: hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v2"},
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAzure,
					Azure:    hyperv1.AzureKMSKeyStatus{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v1"},
				},
				TargetKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAzure,
					Azure:    hyperv1.AzureKMSKeyStatus{KeyVaultName: "vault", KeyName: "key", KeyVersion: "v2"},
				},
			},
			currentConfig: nil,
			validate: func(g Gomega, keys kmsWriteReadKeys) {
				g.Expect(keys.azureWrite.KeyVersion).To(Equal("v1"), "old key should be write during ReadOnlyDeploy")
				g.Expect(keys.azureRead.KeyVersion).To(Equal("v2"), "target key should be read-only during ReadOnlyDeploy")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			keys, err := deriveKMSKeys(tc.kmsSpec, tc.encStatus, tc.currentConfig, tc.kasConverged)
			g.Expect(err).ToNot(HaveOccurred())
			tc.validate(g, keys)
		})
	}
}

func mustAWSProviderName(arn string) string {
	name, _ := kms.AWSKMSProviderName(arn)
	return name
}

func kmsEncryptionConfig(writeProviderName, readProviderName string) *apiserverv1.EncryptionConfiguration {
	providers := []apiserverv1.ProviderConfiguration{
		{KMS: &apiserverv1.KMSConfiguration{Name: writeProviderName, APIVersion: "v2"}},
	}
	if readProviderName != "" {
		providers = append(providers, apiserverv1.ProviderConfiguration{
			KMS: &apiserverv1.KMSConfiguration{Name: readProviderName, APIVersion: "v2"},
		})
	}
	providers = append(providers, apiserverv1.ProviderConfiguration{Identity: &apiserverv1.IdentityConfiguration{}})
	return &apiserverv1.EncryptionConfiguration{
		Resources: []apiserverv1.ResourceConfiguration{
			{Providers: providers},
		},
	}
}
