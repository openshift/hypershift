package kms

import (
	"fmt"
	"hash/fnv"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

func TestNewAWSKMSProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		kmsSpec          *hyperv1.AWSKMSSpec
		kmsImage         string
		tokenMinterImage string
		expectError      bool
	}{
		{
			name:        "When kmsSpec is nil, it should return an error",
			kmsSpec:     nil,
			expectError: true,
		},
		{
			name: "When kmsSpec is valid, it should return a provider with the correct fields",
			kmsSpec: &hyperv1.AWSKMSSpec{
				ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
				Region:    "us-east-1",
			},
			kmsImage:         "quay.io/test/kms:latest",
			tokenMinterImage: "quay.io/test/token-minter:latest",
			expectError:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider, err := NewAWSKMSProvider(tc.kmsSpec, tc.kmsImage, tc.tokenMinterImage)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(provider).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(provider).ToNot(BeNil())
			g.Expect(provider.activeKey).To(Equal(tc.kmsSpec.ActiveKey))
			g.Expect(provider.backupKey).To(Equal(tc.kmsSpec.BackupKey))
			g.Expect(provider.awsRegion).To(Equal(tc.kmsSpec.Region))
			g.Expect(provider.kmsImage).To(Equal(tc.kmsImage))
			g.Expect(provider.tokenMinterImage).To(Equal(tc.tokenMinterImage))
		})
	}
}

func TestGenerateKMSEncryptionConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		provider   func() (*awsKMSProvider, error)
		apiVersion string
		validate   func(g Gomega, config *v1.EncryptionConfiguration, err error)
	}{
		{
			name: "When active key ARN is empty, it should return an error",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: ""},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(config).To(BeNil())
			},
		},
		{
			name: "When only active key is provided, it should generate config with 2 providers (KMS + identity)",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.Resources).To(HaveLen(1))
				g.Expect(config.Resources[0].Providers).To(HaveLen(2))
				g.Expect(config.Resources[0].Providers[0].KMS).ToNot(BeNil())
				g.Expect(config.Resources[0].Providers[1].Identity).ToNot(BeNil())
			},
		},
		{
			name: "When active and backup keys are provided, it should generate config with 3 providers (active KMS + backup KMS + identity)",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/active-key"},
					BackupKey: &hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/backup-key"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.Resources).To(HaveLen(1))
				g.Expect(config.Resources[0].Providers).To(HaveLen(3))
				g.Expect(config.Resources[0].Providers[0].KMS).ToNot(BeNil())
				g.Expect(config.Resources[0].Providers[1].KMS).ToNot(BeNil())
				g.Expect(config.Resources[0].Providers[2].Identity).ToNot(BeNil())
			},
		},
		{
			name: "When backup key ARN is empty, it should only include active KMS provider",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					BackupKey: &hyperv1.AWSKMSKeyEntry{ARN: ""},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.Resources).To(HaveLen(1))
				g.Expect(config.Resources[0].Providers).To(HaveLen(2))
				g.Expect(config.Resources[0].Providers[0].KMS).ToNot(BeNil())
				g.Expect(config.Resources[0].Providers[1].Identity).ToNot(BeNil())
			},
		},
		{
			name: "When called, it should set the correct API version on the encryption config",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.TypeMeta.APIVersion).To(Equal("apiserver.config.k8s.io/v1"))
				g.Expect(config.TypeMeta.Kind).To(Equal(encryptionConfigurationKind))
			},
		},
		{
			name: "When called, it should set timeout to 35 seconds on KMS providers",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/active-key"},
					BackupKey: &hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/backup-key"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				expectedTimeout := &metav1.Duration{Duration: 35 * time.Second}
				for _, provider := range config.Resources[0].Providers {
					if provider.KMS != nil {
						g.Expect(provider.KMS.Timeout).To(Equal(expectedTimeout))
					}
				}
			},
		},
		{
			name: "When called, the KMS provider name should be based on a hash of the ARN",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			apiVersion: "v2",
			validate: func(g Gomega, config *v1.EncryptionConfiguration, err error) {
				g.Expect(err).ToNot(HaveOccurred())

				arn := "arn:aws:kms:us-east-1:123456789:key/test-key-id"
				hasher := fnv.New32()
				_, hashErr := hasher.Write([]byte(arn))
				g.Expect(hashErr).ToNot(HaveOccurred())
				expectedName := fmt.Sprintf("%s-%d", awsKeyNamePrefix, hasher.Sum32())

				g.Expect(config.Resources[0].Providers[0].KMS).ToNot(BeNil())
				g.Expect(config.Resources[0].Providers[0].KMS.Name).To(Equal(expectedName))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider, err := tc.provider()
			g.Expect(err).ToNot(HaveOccurred())

			config, err := provider.GenerateKMSEncryptionConfig(tc.apiVersion)
			tc.validate(g, config, err)
		})
	}
}

func TestGenerateKMSPodConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider func() (*awsKMSProvider, error)
		validate func(g Gomega, podConfig *KMSPodConfig, err error)
	}{
		{
			name: "When active key ARN is empty, it should return an error",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: ""},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			validate: func(g Gomega, podConfig *KMSPodConfig, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(podConfig).To(BeNil())
			},
		},
		{
			name: "When kms image is empty, it should return an error",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "", "quay.io/test/token-minter:latest")
			},
			validate: func(g Gomega, podConfig *KMSPodConfig, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(podConfig).To(BeNil())
			},
		},
		{
			name: "When valid config is provided without backup key, it should return 2 containers (token-minter + active)",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			validate: func(g Gomega, podConfig *KMSPodConfig, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(podConfig.Containers).To(HaveLen(2))
				g.Expect(podConfig.Containers[0].Name).To(Equal("aws-kms-token-minter"))
				g.Expect(podConfig.Containers[1].Name).To(Equal("aws-kms-active"))
			},
		},
		{
			name: "When valid config is provided with backup key, it should return 3 containers (token-minter + active + backup)",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/active-key"},
					BackupKey: &hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/backup-key"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			validate: func(g Gomega, podConfig *KMSPodConfig, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(podConfig.Containers).To(HaveLen(3))
				g.Expect(podConfig.Containers[0].Name).To(Equal("aws-kms-token-minter"))
				g.Expect(podConfig.Containers[1].Name).To(Equal("aws-kms-active"))
				g.Expect(podConfig.Containers[2].Name).To(Equal("aws-kms-backup"))
			},
		},
		{
			name: "When valid config is provided, it should return 3 volumes",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			validate: func(g Gomega, podConfig *KMSPodConfig, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(podConfig.Volumes).To(HaveLen(3))
				g.Expect(podConfig.Volumes[0].Name).To(Equal("aws-kms-credentials"))
				g.Expect(podConfig.Volumes[1].Name).To(Equal("kms-socket"))
				g.Expect(podConfig.Volumes[2].Name).To(Equal("aws-kms-token"))
			},
		},
		{
			name: "When valid config is provided, it should set KASContainerMutate function",
			provider: func() (*awsKMSProvider, error) {
				return NewAWSKMSProvider(&hyperv1.AWSKMSSpec{
					ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789:key/test-key-id"},
					Region:    "us-east-1",
				}, "quay.io/test/kms:latest", "quay.io/test/token-minter:latest")
			},
			validate: func(g Gomega, podConfig *KMSPodConfig, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(podConfig.KASContainerMutate).ToNot(BeNil())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider, err := tc.provider()
			g.Expect(err).ToNot(HaveOccurred())

			podConfig, err := provider.GenerateKMSPodConfig()
			tc.validate(g, podConfig, err)
		})
	}
}
