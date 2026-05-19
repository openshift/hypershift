package kas

import (
	"bytes"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
)

const (
	kmsAPIVersionV1 = "v1"
	kmsAPIVersionV2 = "v2"
)

func TestReconcileKMSEncryptionConfigAWS(t *testing.T) {
	encryptionSpec := &hyperv1.KMSSpec{Provider: hyperv1.AWS, AWS: &hyperv1.AWSKMSSpec{
		ActiveKey: hyperv1.AWSKMSKeyEntry{ARN: "test"},
	}}

	testCases := []struct {
		name           string
		config         *v1.EncryptionConfiguration
		expectedConfig *v1.EncryptionConfiguration
	}{
		{
			name:           "No encryption config",
			expectedConfig: generateExpectedEncryptionConfig(kmsAPIVersionV2),
		},
		{
			name: "Encryption config with Identity provider",
			config: &v1.EncryptionConfiguration{
				TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
				Resources: []v1.ResourceConfiguration{
					{
						Resources: config.KMSEncryptedObjects(),
						Providers: []v1.ProviderConfiguration{
							{
								Identity: &v1.IdentityConfiguration{},
							},
						},
					},
				},
			},
			expectedConfig: generateExpectedEncryptionConfig(kmsAPIVersionV2),
		},
		{
			name: "KMS v1 encryption config",
			config: &v1.EncryptionConfiguration{
				TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
				Resources: []v1.ResourceConfiguration{
					{
						Resources: config.KMSEncryptedObjects(),
						Providers: []v1.ProviderConfiguration{
							{
								KMS: &v1.KMSConfiguration{
									APIVersion: "v1",
								},
							},
						},
					},
				},
			},
			expectedConfig: generateExpectedEncryptionConfig(kmsAPIVersionV1),
		},
		{
			name: "KMS v2 encryption config",
			config: &v1.EncryptionConfiguration{
				TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
				Resources: []v1.ResourceConfiguration{
					{
						Resources: config.KMSEncryptedObjects(),
						Providers: []v1.ProviderConfiguration{
							{
								KMS: &v1.KMSConfiguration{
									APIVersion: "v2",
								},
							},
						},
					},
				},
			},
			expectedConfig: generateExpectedEncryptionConfig(kmsAPIVersionV2),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encryptionConfigFile := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret-encryption-config",
					Namespace: "test-namespace",
				},
				Data: make(map[string][]byte),
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.config != nil {
				buff := bytes.NewBuffer([]byte{})
				err := api.YamlSerializer.Encode(tc.config, buff)
				if err != nil {
					t.Errorf("failed to encode encryption config: %v", err)
				}
				encryptionConfigFile.Data[secretEncryptionConfigurationKey] = buff.Bytes()

				clientBuilder.WithObjects(encryptionConfigFile)
			}

			cpContext := controlplanecomponent.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						SecretEncryption: &hyperv1.SecretEncryptionSpec{
							Type: hyperv1.KMS,
							KMS:  encryptionSpec,
						},
					},
				},
				Client: clientBuilder.Build(),
			}
			err := adaptSecretEncryptionConfig(cpContext, encryptionConfigFile)
			if err != nil {
				t.Errorf("failed to reconcile KMS encryption config: %v", err)
			}

			encryptionConfigBytes := encryptionConfigFile.Data[secretEncryptionConfigurationKey]
			if len(encryptionConfigBytes) == 0 {
				t.Error("reconciled empty encryption config")
			}
			config := v1.EncryptionConfiguration{}
			gvks, _, err := api.Scheme.ObjectKinds(&config)
			if err != nil || len(gvks) == 0 {
				t.Errorf("cannot determine gvk of resource: %v", err)
			}
			if _, _, err = api.YamlSerializer.Decode(encryptionConfigBytes, &gvks[0], &config); err != nil {
				t.Errorf("cannot decode resource: %v", err)
			}

			if diff := cmp.Diff(config, *tc.expectedConfig); diff != "" {
				t.Errorf("reconciled encryption config differs from expected: %s", diff)
			}
		})
	}
}

func TestReconcileKMSEncryptionConfigAzure(t *testing.T) {
	encryptionSpec := &hyperv1.KMSSpec{Provider: hyperv1.AZURE, Azure: &hyperv1.AzureKMSSpec{
		ActiveKey: hyperv1.AzureKMSKey{
			KeyVaultName: "test-vault",
			KeyName:      "test-key",
			KeyVersion:   "test-version",
		},
	}}

	testCases := []struct {
		name           string
		config         *v1.EncryptionConfiguration
		expectedConfig *v1.EncryptionConfiguration
	}{
		{
			name:           "When no existing encryption config it should generate v2 config",
			expectedConfig: generateExpectedAzureEncryptionConfig(t, kmsAPIVersionV2),
		},
		{
			name: "When existing KMS v1 config it should preserve v1",
			config: &v1.EncryptionConfiguration{
				TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
				Resources: []v1.ResourceConfiguration{
					{
						Resources: config.KMSEncryptedObjects(),
						Providers: []v1.ProviderConfiguration{
							{KMS: &v1.KMSConfiguration{APIVersion: "v1"}},
						},
					},
				},
			},
			expectedConfig: generateExpectedAzureEncryptionConfig(t, kmsAPIVersionV1),
		},
		{
			name: "When existing KMS v2 config it should preserve v2",
			config: &v1.EncryptionConfiguration{
				TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
				Resources: []v1.ResourceConfiguration{
					{
						Resources: config.KMSEncryptedObjects(),
						Providers: []v1.ProviderConfiguration{
							{KMS: &v1.KMSConfiguration{APIVersion: "v2"}},
						},
					},
				},
			},
			expectedConfig: generateExpectedAzureEncryptionConfig(t, kmsAPIVersionV2),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encryptionConfigFile := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret-encryption-config",
					Namespace: "test-namespace",
				},
				Data: make(map[string][]byte),
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.config != nil {
				buff := bytes.NewBuffer([]byte{})
				err := api.YamlSerializer.Encode(tc.config, buff)
				if err != nil {
					t.Errorf("failed to encode encryption config: %v", err)
				}
				encryptionConfigFile.Data[secretEncryptionConfigurationKey] = buff.Bytes()
				clientBuilder.WithObjects(encryptionConfigFile)
			}

			cpContext := controlplanecomponent.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						SecretEncryption: &hyperv1.SecretEncryptionSpec{
							Type: hyperv1.KMS,
							KMS:  encryptionSpec,
						},
					},
				},
				Client: clientBuilder.Build(),
			}
			err := adaptSecretEncryptionConfig(cpContext, encryptionConfigFile)
			if err != nil {
				t.Errorf("failed to reconcile KMS encryption config: %v", err)
			}

			encryptionConfigBytes := encryptionConfigFile.Data[secretEncryptionConfigurationKey]
			if len(encryptionConfigBytes) == 0 {
				t.Error("reconciled empty encryption config")
			}
			encConfig := v1.EncryptionConfiguration{}
			gvks, _, err := api.Scheme.ObjectKinds(&encConfig)
			if err != nil || len(gvks) == 0 {
				t.Errorf("cannot determine gvk of resource: %v", err)
			}
			if _, _, err = api.YamlSerializer.Decode(encryptionConfigBytes, &gvks[0], &encConfig); err != nil {
				t.Errorf("cannot decode resource: %v", err)
			}

			if diff := cmp.Diff(encConfig, *tc.expectedConfig); diff != "" {
				t.Errorf("reconciled encryption config differs from expected: %s", diff)
			}
		})
	}
}

func TestReconcileKMSEncryptionConfigAzureSelfManaged(t *testing.T) {
	encryptionSpec := &hyperv1.KMSSpec{Provider: hyperv1.AZURE, Azure: &hyperv1.AzureKMSSpec{
		ActiveKey: hyperv1.AzureKMSKey{
			KeyVaultName: "test-vault",
			KeyName:      "test-key",
			KeyVersion:   "test-version",
		},
		WorkloadIdentity: hyperv1.WorkloadIdentity{
			ClientID: "kms-client-id",
		},
	}}

	testCases := []struct {
		name           string
		config         *v1.EncryptionConfiguration
		expectedConfig *v1.EncryptionConfiguration
	}{
		{
			name:           "When self-managed Azure with no existing config it should generate v2 encryption config",
			expectedConfig: generateExpectedAzureEncryptionConfig(t, kmsAPIVersionV2),
		},
		{
			name: "When self-managed Azure with existing v1 config it should preserve v1",
			config: &v1.EncryptionConfiguration{
				TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
				Resources: []v1.ResourceConfiguration{
					{
						Resources: config.KMSEncryptedObjects(),
						Providers: []v1.ProviderConfiguration{
							{KMS: &v1.KMSConfiguration{APIVersion: "v1"}},
						},
					},
				},
			},
			expectedConfig: generateExpectedAzureEncryptionConfig(t, kmsAPIVersionV1),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encryptionConfigFile := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret-encryption-config",
					Namespace: "test-namespace",
				},
				Data: make(map[string][]byte),
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.config != nil {
				buff := bytes.NewBuffer([]byte{})
				err := api.YamlSerializer.Encode(tc.config, buff)
				if err != nil {
					t.Fatalf("failed to encode encryption config: %v", err)
				}
				encryptionConfigFile.Data[secretEncryptionConfigurationKey] = buff.Bytes()
				clientBuilder.WithObjects(encryptionConfigFile)
			}

			cpContext := controlplanecomponent.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AzurePlatform,
							Azure: &hyperv1.AzurePlatformSpec{
								TenantID: "test-tenant-id",
							},
						},
						SecretEncryption: &hyperv1.SecretEncryptionSpec{
							Type: hyperv1.KMS,
							KMS:  encryptionSpec,
						},
					},
				},
				Client: clientBuilder.Build(),
			}
			err := adaptSecretEncryptionConfig(cpContext, encryptionConfigFile)
			if err != nil {
				t.Fatalf("failed to reconcile KMS encryption config: %v", err)
			}

			encryptionConfigBytes := encryptionConfigFile.Data[secretEncryptionConfigurationKey]
			if len(encryptionConfigBytes) == 0 {
				t.Fatal("reconciled empty encryption config")
			}
			encConfig := v1.EncryptionConfiguration{}
			gvks, _, err := api.Scheme.ObjectKinds(&encConfig)
			if err != nil || len(gvks) == 0 {
				t.Fatalf("cannot determine gvk of resource: %v", err)
			}
			if _, _, err = api.YamlSerializer.Decode(encryptionConfigBytes, &gvks[0], &encConfig); err != nil {
				t.Fatalf("cannot decode resource: %v", err)
			}

			if diff := cmp.Diff(encConfig, *tc.expectedConfig); diff != "" {
				t.Errorf("reconciled encryption config differs from expected: %s", diff)
			}
		})
	}
}

func generateExpectedAzureEncryptionConfig(t testing.TB, apiVersion string) *v1.EncryptionConfiguration {
	t.Helper()
	activeKeyHash, err := util.HashStruct(hyperv1.AzureKMSKey{
		KeyVaultName: "test-vault",
		KeyName:      "test-key",
		KeyVersion:   "test-version",
	})
	if err != nil {
		t.Fatalf("failed to hash Azure KMS key: %v", err)
	}
	return &v1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
		Resources: []v1.ResourceConfiguration{
			{
				Resources: config.KMSEncryptedObjects(),
				Providers: []v1.ProviderConfiguration{
					{
						KMS: &v1.KMSConfiguration{
							APIVersion: apiVersion,
							Name:       "azure-" + activeKeyHash,
							Endpoint:   "unix:///opt/azurekmsactive.socket",
							Timeout:    &metav1.Duration{Duration: 35 * time.Second},
						},
					},
					{Identity: &v1.IdentityConfiguration{}},
				},
			},
		},
	}
}

func generateExpectedEncryptionConfig(apiVersion string) *v1.EncryptionConfiguration {
	config := &v1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
		Resources: []v1.ResourceConfiguration{
			{
				Resources: config.KMSEncryptedObjects(),
				Providers: []v1.ProviderConfiguration{
					{
						KMS: &v1.KMSConfiguration{
							APIVersion: apiVersion,
							Name:       "awskmskey-3157003241",
							Endpoint:   "unix:///var/run/awskmsactive.sock",
							Timeout:    &metav1.Duration{Duration: 35 * time.Second},
						},
					},
					{
						Identity: &v1.IdentityConfiguration{},
					},
				},
			},
		},
	}

	if apiVersion == kmsAPIVersionV1 {
		config.Resources[0].Providers[0].KMS.CacheSize = ptr.To[int32](100)
	}

	return config
}
