package kas

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
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

			kasDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "kube-apiserver",
					Generation: 1,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration:  1,
					Replicas:            1,
					UpdatedReplicas:     1,
					ReadyReplicas:       1,
					AvailableReplicas:   1,
					UnavailableReplicas: 0,
				},
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(kasDeployment)
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

func TestGetKMSAPIVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		config     *v1.EncryptionConfiguration
		wantResult string
	}{
		{
			name:       "When config is nil it should return default v2",
			config:     nil,
			wantResult: "v2",
		},
		{
			name: "When config has no KMS provider it should return default v2",
			config: &v1.EncryptionConfiguration{
				Resources: []v1.ResourceConfiguration{
					{
						Providers: []v1.ProviderConfiguration{
							{Identity: &v1.IdentityConfiguration{}},
						},
					},
				},
			},
			wantResult: "v2",
		},
		{
			name: "When config has KMS v1 provider it should return v1",
			config: &v1.EncryptionConfiguration{
				Resources: []v1.ResourceConfiguration{
					{
						Providers: []v1.ProviderConfiguration{
							{KMS: &v1.KMSConfiguration{APIVersion: "v1"}},
						},
					},
				},
			},
			wantResult: "v1",
		},
		{
			name: "When config has KMS v2 provider it should return v2",
			config: &v1.EncryptionConfiguration{
				Resources: []v1.ResourceConfiguration{
					{
						Providers: []v1.ProviderConfiguration{
							{KMS: &v1.KMSConfiguration{APIVersion: "v2"}},
						},
					},
				},
			},
			wantResult: "v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := getKMSAPIVersion(tt.config)
			g.Expect(result).To(Equal(tt.wantResult))
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

func TestDeriveAESCBCEncryptionConfig(t *testing.T) {
	t.Parallel()

	const testNamespace = "test-namespace"

	newAESCBCKeySecret := func(name string, keyData []byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Data:       map[string][]byte{hyperv1.AESCBCKeySecretKey: keyData},
		}
	}

	convergedKASDeployment := func() *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "kube-apiserver",
				Namespace:  testNamespace,
				Generation: 1,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](1),
			},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration:  1,
				Replicas:            1,
				UpdatedReplicas:     1,
				ReadyReplicas:       1,
				AvailableReplicas:   1,
				UnavailableReplicas: 0,
			},
		}
	}

	decodeEncryptionConfig := func(g Gomega, data []byte) *v1.EncryptionConfiguration {
		cfg := &v1.EncryptionConfiguration{}
		gvks, _, err := api.Scheme.ObjectKinds(cfg)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(gvks).NotTo(BeEmpty())
		_, _, err = api.YamlSerializer.Decode(data, &gvks[0], cfg)
		g.Expect(err).NotTo(HaveOccurred())
		return cfg
	}

	testCases := []struct {
		name          string
		secretObjects []*corev1.Secret
		secretSpec    *hyperv1.SecretEncryptionSpec
		encStatus     *hyperv1.SecretEncryptionStatus
		currentConfig *v1.EncryptionConfiguration
		kasConverged  bool
		verify        func(g Gomega, data []byte)
	}{
		{
			name: "When status has no active key it should use spec active key as write key",
			secretObjects: []*corev1.Secret{
				newAESCBCKeySecret("aescbc-key-1", []byte("active-key-data")),
			},
			secretSpec: &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.AESCBC,
				AESCBC: &hyperv1.AESCBCSpec{
					ActiveKey: corev1.LocalObjectReference{Name: "aescbc-key-1"},
				},
			},
			encStatus: nil,
			verify: func(g Gomega, data []byte) {
				cfg := decodeEncryptionConfig(g, data)
				g.Expect(cfg.Resources).To(HaveLen(1))
				providers := cfg.Resources[0].Providers
				g.Expect(providers).To(HaveLen(2))
				g.Expect(providers[0].AESCBC).NotTo(BeNil())
				g.Expect(providers[0].AESCBC.Keys).To(HaveLen(1))

				expectedName, err := AESCBCKeyName([]byte("active-key-data"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(providers[0].AESCBC.Keys[0].Name).To(Equal(expectedName))
				g.Expect(providers[0].AESCBC.Keys[0].Secret).To(Equal(base64.StdEncoding.EncodeToString([]byte("active-key-data"))))

				g.Expect(providers[1].Identity).NotTo(BeNil())
			},
		},
		{
			name: "When status has no active key and backup key is set it should use both keys",
			secretObjects: []*corev1.Secret{
				newAESCBCKeySecret("aescbc-key-1", []byte("active-key-data")),
				newAESCBCKeySecret("aescbc-backup", []byte("backup-key-data")),
			},
			secretSpec: &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.AESCBC,
				AESCBC: &hyperv1.AESCBCSpec{
					ActiveKey: corev1.LocalObjectReference{Name: "aescbc-key-1"},
					BackupKey: &corev1.LocalObjectReference{Name: "aescbc-backup"},
				},
			},
			encStatus: nil,
			verify: func(g Gomega, data []byte) {
				cfg := decodeEncryptionConfig(g, data)
				g.Expect(cfg.Resources).To(HaveLen(1))
				providers := cfg.Resources[0].Providers
				g.Expect(providers).To(HaveLen(2))
				g.Expect(providers[0].AESCBC).NotTo(BeNil())
				g.Expect(providers[0].AESCBC.Keys).To(HaveLen(2))

				activeKeyName, err := AESCBCKeyName([]byte("active-key-data"))
				g.Expect(err).NotTo(HaveOccurred())
				backupKeyName, err := AESCBCKeyName([]byte("backup-key-data"))
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(providers[0].AESCBC.Keys[0].Name).To(Equal(activeKeyName))
				g.Expect(providers[0].AESCBC.Keys[1].Name).To(Equal(backupKeyName))
			},
		},
		{
			name: "When no rotation in progress it should use spec active key only",
			secretObjects: []*corev1.Secret{
				newAESCBCKeySecret("aescbc-key-1", []byte("active-key-data")),
			},
			secretSpec: &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.AESCBC,
				AESCBC: &hyperv1.AESCBCSpec{
					ActiveKey: corev1.LocalObjectReference{Name: "aescbc-key-1"},
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAESCBC,
					AESCBC: hyperv1.AESCBCKeyStatus{
						Secret:   hyperv1.SecretReference{Name: "aescbc-key-1"},
						DataHash: "activehash",
					},
				},
			},
			verify: func(g Gomega, data []byte) {
				cfg := decodeEncryptionConfig(g, data)
				g.Expect(cfg.Resources).To(HaveLen(1))
				providers := cfg.Resources[0].Providers
				g.Expect(providers).To(HaveLen(2))
				g.Expect(providers[0].AESCBC).NotTo(BeNil())
				g.Expect(providers[0].AESCBC.Keys).To(HaveLen(1))

				expectedName, err := AESCBCKeyName([]byte("active-key-data"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(providers[0].AESCBC.Keys[0].Name).To(Equal(expectedName))
			},
		},
		{
			name: "When rotation in progress and target key is read-only it should keep old key as write",
			secretObjects: []*corev1.Secret{
				newAESCBCKeySecret("old-key-secret", []byte("old-key-data")),
				newAESCBCKeySecret("new-key-secret", []byte("new-key-data")),
			},
			secretSpec: &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.AESCBC,
				AESCBC: &hyperv1.AESCBCSpec{
					ActiveKey: corev1.LocalObjectReference{Name: "new-key-secret"},
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAESCBC,
					AESCBC: hyperv1.AESCBCKeyStatus{
						Secret:   hyperv1.SecretReference{Name: "old-key-secret"},
						DataHash: "oldhash",
					},
				},
				TargetKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAESCBC,
					AESCBC: hyperv1.AESCBCKeyStatus{
						Secret:   hyperv1.SecretReference{Name: "new-key-secret"},
						DataHash: "newhash",
					},
				},
			},
			currentConfig: func() *v1.EncryptionConfiguration {
				oldKeyName, _ := AESCBCKeyName([]byte("old-key-data"))
				targetKeyName, _ := AESCBCKeyName([]byte("new-key-data"))
				return &v1.EncryptionConfiguration{
					Resources: []v1.ResourceConfiguration{{
						Providers: []v1.ProviderConfiguration{
							{AESCBC: &v1.AESConfiguration{Keys: []v1.Key{
								{Name: oldKeyName, Secret: base64.StdEncoding.EncodeToString([]byte("old-key-data"))},
								{Name: targetKeyName, Secret: base64.StdEncoding.EncodeToString([]byte("new-key-data"))},
							}}},
							{Identity: &v1.IdentityConfiguration{}},
						},
					}},
				}
			}(),
			kasConverged: false,
			verify: func(g Gomega, data []byte) {
				cfg := decodeEncryptionConfig(g, data)
				g.Expect(cfg.Resources).To(HaveLen(1))
				providers := cfg.Resources[0].Providers
				g.Expect(providers[0].AESCBC).NotTo(BeNil())
				g.Expect(providers[0].AESCBC.Keys).To(HaveLen(2))

				oldKeyName, err := AESCBCKeyName([]byte("old-key-data"))
				g.Expect(err).NotTo(HaveOccurred())
				targetKeyName, err := AESCBCKeyName([]byte("new-key-data"))
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(providers[0].AESCBC.Keys[0].Name).To(Equal(oldKeyName), "old key should remain the write key")
				g.Expect(providers[0].AESCBC.Keys[1].Name).To(Equal(targetKeyName), "target key should be read-only")
			},
		},
		{
			name: "When rotation in progress and target key should be promoted it should swap keys",
			secretObjects: []*corev1.Secret{
				newAESCBCKeySecret("old-key-secret", []byte("old-key-data")),
				newAESCBCKeySecret("new-key-secret", []byte("new-key-data")),
			},
			secretSpec: &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.AESCBC,
				AESCBC: &hyperv1.AESCBCSpec{
					ActiveKey: corev1.LocalObjectReference{Name: "new-key-secret"},
				},
			},
			encStatus: &hyperv1.SecretEncryptionStatus{
				ActiveKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAESCBC,
					AESCBC: hyperv1.AESCBCKeyStatus{
						Secret:   hyperv1.SecretReference{Name: "old-key-secret"},
						DataHash: "oldhash",
					},
				},
				TargetKey: hyperv1.SecretEncryptionKeyStatus{
					Provider: hyperv1.SecretEncryptionProviderAESCBC,
					AESCBC: hyperv1.AESCBCKeyStatus{
						Secret:   hyperv1.SecretReference{Name: "new-key-secret"},
						DataHash: "newhash",
					},
				},
			},
			currentConfig: func() *v1.EncryptionConfiguration {
				oldKeyName, _ := AESCBCKeyName([]byte("old-key-data"))
				targetKeyName, _ := AESCBCKeyName([]byte("new-key-data"))
				return &v1.EncryptionConfiguration{
					Resources: []v1.ResourceConfiguration{{
						Providers: []v1.ProviderConfiguration{
							{AESCBC: &v1.AESConfiguration{Keys: []v1.Key{
								{Name: oldKeyName, Secret: base64.StdEncoding.EncodeToString([]byte("old-key-data"))},
								{Name: targetKeyName, Secret: base64.StdEncoding.EncodeToString([]byte("new-key-data"))},
							}}},
							{Identity: &v1.IdentityConfiguration{}},
						},
					}},
				}
			}(),
			kasConverged: true,
			verify: func(g Gomega, data []byte) {
				cfg := decodeEncryptionConfig(g, data)
				g.Expect(cfg.Resources).To(HaveLen(1))
				providers := cfg.Resources[0].Providers
				g.Expect(providers[0].AESCBC).NotTo(BeNil())
				g.Expect(providers[0].AESCBC.Keys).To(HaveLen(2))

				oldKeyName, err := AESCBCKeyName([]byte("old-key-data"))
				g.Expect(err).NotTo(HaveOccurred())
				targetKeyName, err := AESCBCKeyName([]byte("new-key-data"))
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(providers[0].AESCBC.Keys[0].Name).To(Equal(targetKeyName), "target key should be promoted to write key")
				g.Expect(providers[0].AESCBC.Keys[1].Name).To(Equal(oldKeyName), "old key should become read-only")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			kasDeployment := convergedKASDeployment()
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(kasDeployment)
			for _, s := range tc.secretObjects {
				clientBuilder.WithObjects(s)
			}

			cpContext := controlplanecomponent.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testNamespace,
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						SecretEncryption: tc.secretSpec,
					},
				},
				Client: clientBuilder.Build(),
			}

			data, err := deriveAESCBCEncryptionConfig(cpContext, tc.secretSpec, tc.encStatus, tc.currentConfig, tc.kasConverged)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(data).NotTo(BeEmpty())

			tc.verify(g, data)
		})
	}
}
