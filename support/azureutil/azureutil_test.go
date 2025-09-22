package azureutil

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestGetSubnetNameFromSubnetID(t *testing.T) {
	tests := []struct {
		testCaseName       string
		subnetID           string
		expectedSubnetName string
		expectedErr        bool
	}{
		{
			testCaseName:       "empty subnet ID",
			subnetID:           "",
			expectedSubnetName: "",
			expectedErr:        true,
		},
		{
			testCaseName:       "improperly formed subnet ID",
			subnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets",
			expectedSubnetName: "",
			expectedErr:        true,
		},
		{
			testCaseName:       "properly formed subnet ID",
			subnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
			expectedSubnetName: "mySubnetName",
			expectedErr:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			subnetID, err := GetSubnetNameFromSubnetID(tc.subnetID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid subnet ID format: "+tc.subnetID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(subnetID).To(Equal(tc.expectedSubnetName))
			}
		})
	}
}

func TestGetNetworkSecurityGroupNameFromNetworkSecurityGroupID(t *testing.T) {
	tests := []struct {
		testCaseName    string
		nsgID           string
		expectedNSGName string
		expectedNSGRG   string
		expectedErr     bool
	}{
		{
			testCaseName:    "empty NSG ID",
			nsgID:           "",
			expectedNSGName: "",
			expectedNSGRG:   "",
			expectedErr:     true,
		},
		{
			testCaseName:    "improperly formed nsg ID",
			nsgID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups",
			expectedNSGName: "",
			expectedNSGRG:   "",
			expectedErr:     true,
		},
		{
			testCaseName:    "properly formed nsg ID",
			nsgID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
			expectedNSGName: "myNSGName",
			expectedNSGRG:   "myResourceGroupName",
			expectedErr:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			nsgName, nsgRG, err := GetNameAndResourceGroupFromNetworkSecurityGroupID(tc.nsgID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid nsg ID format: "+tc.nsgID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(nsgName).To(Equal(tc.expectedNSGName))
				g.Expect(nsgRG).To(Equal(tc.expectedNSGRG))
			}
		})
	}
}

func TestGetVnetNameAndResourceGroupFromVnetID(t *testing.T) {
	tests := []struct {
		testCaseName     string
		vnetID           string
		expectedVnetName string
		expectedVnetRG   string
		expectedErr      bool
	}{
		{
			testCaseName:     "empty VNET ID",
			vnetID:           "",
			expectedVnetName: "",
			expectedVnetRG:   "",
			expectedErr:      true,
		},
		{
			testCaseName:     "improperly formed VNET ID",
			vnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/",
			expectedVnetName: "",
			expectedVnetRG:   "",
			expectedErr:      true,
		},
		{
			testCaseName:     "properly formed VNET ID",
			vnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName",
			expectedVnetName: "myVnetName",
			expectedVnetRG:   "myResourceGroupName",
			expectedErr:      false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			vnetName, vnetRG, err := GetVnetNameAndResourceGroupFromVnetID(tc.vnetID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid VNET ID format: "+tc.vnetID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(vnetName).To(Equal(tc.expectedVnetName))
				g.Expect(vnetRG).To(Equal(tc.expectedVnetRG))
			}
		})
	}
}

func TestIsAroHCP(t *testing.T) {
	testCases := []struct {
		name          string
		envVarValue   string
		expectedValue bool
	}{
		{
			name:          "Sets the managed service env var to hyperv1.AroHCP so the function should return true",
			envVarValue:   hyperv1.AroHCP,
			expectedValue: true,
		},
		{
			name:          "Sets the managed service env var to nothing so the function should return false",
			envVarValue:   "",
			expectedValue: false,
		},
		{
			name:          "Sets the managed service env var to 'asdf' so the function should return false",
			envVarValue:   "asdf",
			expectedValue: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			t.Setenv("MANAGED_SERVICE", tc.envVarValue)
			isAroHcp := IsAroHCP()
			g.Expect(isAroHcp).To(Equal(tc.expectedValue))
		})
	}
}

func TestCreateEnvVarsForAzureManagedIdentity(t *testing.T) {
	type args struct {
		azureCredentialsFilepath string
	}
	tests := []struct {
		name string
		args args
		want []corev1.EnvVar
	}{
		{
			name: "returns a slice of environment variables with the azure creds",
			args: args{
				azureCredentialsFilepath: "my-credentials-file",
			},
			want: []corev1.EnvVar{
				{
					Name:  config.ManagedAzureCredentialsFilePath,
					Value: config.ManagedAzureCertificatePath + "my-credentials-file",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateEnvVarsForAzureManagedIdentity(tt.args.azureCredentialsFilepath); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateEnvVarsForAzureManagedIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateVolumeMountForAzureSecretStoreProviderClass(t *testing.T) {
	tests := []struct {
		name                  string
		secretStoreVolumeName string
		want                  corev1.VolumeMount
	}{
		{
			name:                  "return a volume mount for a secret store provider",
			secretStoreVolumeName: "my-secret-store",
			want: corev1.VolumeMount{
				Name:      "my-secret-store",
				MountPath: config.ManagedAzureCertificateMountPath,
				ReadOnly:  true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateVolumeMountForAzureSecretStoreProviderClass(tt.secretStoreVolumeName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateVolumeMountForAzureSecretStoreProviderClass() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateVolumeForAzureSecretStoreProviderClass(t *testing.T) {
	tests := []struct {
		name                    string
		secretStoreVolumeName   string
		secretProviderClassName string
		want                    corev1.Volume
	}{
		{
			name:                    "return a volume for a secret store provider",
			secretStoreVolumeName:   "my-secret-store",
			secretProviderClassName: "my-secret-provider-class",
			want: corev1.Volume{
				Name: "my-secret-store",
				VolumeSource: corev1.VolumeSource{
					CSI: &corev1.CSIVolumeSource{
						Driver:   config.ManagedAzureSecretsStoreCSIDriver,
						ReadOnly: ptr.To(true),
						VolumeAttributes: map[string]string{
							config.ManagedAzureSecretProviderClass: "my-secret-provider-class",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateVolumeForAzureSecretStoreProviderClass(tt.secretStoreVolumeName, tt.secretProviderClassName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateVolumeForAzureSecretStoreProviderClass() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAzureEncryptionKeyInfo(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		wantVaultHost  string
		wantKeyName    string
		wantKeyVersion string
		wantErr        bool
	}{
		{
			name:           "When given a valid key id it should parse fields",
			id:             "https://example-kms.vault.azure.net/keys/example-key/1234abcd",
			wantVaultHost:  "example-kms",
			wantKeyName:    "example-key",
			wantKeyVersion: "1234abcd",
			wantErr:        false,
		},
		{
			name:    "When key id missing version it should error",
			id:      "https://example-kms.vault.azure.net/keys/example-key",
			wantErr: true,
		},
		{
			name:    "When key id path not under /keys it should error",
			id:      "https://example-kms.vault.azure.net/secrets/example-key/1234abcd",
			wantErr: true,
		},
		{
			name:    "When key id has trailing slash it should error",
			id:      "https://example-kms.vault.azure.net/keys/example-key/1234abcd/",
			wantErr: true,
		},
		{
			name:           "Parses govcloud suffix correctly",
			id:             "https://example-kms.vault.usgovcloudapi.net/keys/example-key/1234abcd",
			wantVaultHost:  "example-kms",
			wantKeyName:    "example-key",
			wantKeyVersion: "1234abcd",
			wantErr:        false,
		},
		{
			name:    "Missing scheme should error",
			id:      "example-kms.vault.azure.net/keys/example-key/1234abcd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			got, err := GetAzureEncryptionKeyInfo(tt.id)
			if tt.wantErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).To(BeNil())
			g.Expect(got).To(Not(BeNil()))
			g.Expect(got.KeyVaultName).To(Equal(tt.wantVaultHost))
			g.Expect(got.KeyName).To(Equal(tt.wantKeyName))
			g.Expect(got.KeyVersion).To(Equal(tt.wantKeyVersion))
		})
	}
}

func TestReconcileAzureCredentials(t *testing.T) {
	g := NewWithT(t)

	baseSecretData := map[string][]byte{
		"azure_region":          []byte("eastus"),
		"azure_resource_prefix": []byte("test-cluster-abcd"),
		"azure_resourcegroup":   []byte("test-rg"),
		"azure_subscription_id": []byte("sub-123"),
		"azure_tenant_id":       []byte("tenant-456"),
	}

	// Helper function to create a secret manifest
	createTestSecretManifest := func(name, namespace string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
	}

	tests := []struct {
		name                 string
		configs              []AzureCredentialConfig
		capabilities         *hyperv1.Capabilities
		expectedSecretsCount int
		expectedErrors       int
		validateSecret       func(secret *corev1.Secret, config AzureCredentialConfig)
	}{
		{
			name: "creates all secrets with correct client IDs when all capabilities enabled",
			configs: []AzureCredentialConfig{
				{
					Name:         "ingress",
					ManifestFunc: func() *corev1.Secret { return createTestSecretManifest("ingress-creds", "test-ns") },
					ClientID:     "ingress-client-id",
					CapabilityChecker: func(caps *hyperv1.Capabilities) bool {
						return caps == nil || !isCapabilityDisabled(caps, hyperv1.IngressCapability)
					},
					ErrorContext: "ingress credentials",
				},
				{
					Name:              "disk-csi",
					ManifestFunc:      func() *corev1.Secret { return createTestSecretManifest("disk-creds", "test-ns") },
					ClientID:          "disk-client-id",
					CapabilityChecker: nil, // Always enabled
					ErrorContext:      "disk CSI credentials",
				},
			},
			capabilities:         &hyperv1.Capabilities{},
			expectedSecretsCount: 2,
			expectedErrors:       0,
			validateSecret: func(secret *corev1.Secret, config AzureCredentialConfig) {
				g.Expect(secret.Data["azure_client_id"]).To(Equal([]byte(config.ClientID)))
				g.Expect(secret.Data["azure_region"]).To(Equal([]byte("eastus")))
			},
		},
		{
			name: "skips secrets when capability is disabled",
			configs: []AzureCredentialConfig{
				{
					Name:         "ingress",
					ManifestFunc: func() *corev1.Secret { return createTestSecretManifest("ingress-creds", "test-ns") },
					ClientID:     "ingress-client-id",
					CapabilityChecker: func(caps *hyperv1.Capabilities) bool {
						return caps == nil || !isCapabilityDisabled(caps, hyperv1.IngressCapability)
					},
					ErrorContext: "ingress credentials",
				},
				{
					Name:              "disk-csi",
					ManifestFunc:      func() *corev1.Secret { return createTestSecretManifest("disk-creds", "test-ns") },
					ClientID:          "disk-client-id",
					CapabilityChecker: nil, // Always enabled
					ErrorContext:      "disk CSI credentials",
				},
			},
			capabilities:         &hyperv1.Capabilities{Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability}},
			expectedSecretsCount: 1, // Only disk-csi should be created
			expectedErrors:       0,
			validateSecret: func(secret *corev1.Secret, config AzureCredentialConfig) {
				if config.Name == "disk-csi" {
					g.Expect(secret.Data["azure_client_id"]).To(Equal([]byte(config.ClientID)))
				}
			},
		},
		{
			name: "creates secrets without client ID when not provided",
			configs: []AzureCredentialConfig{
				{
					Name:              "test-secret",
					ManifestFunc:      func() *corev1.Secret { return createTestSecretManifest("test-creds", "test-ns") },
					ClientID:          "", // Empty client ID
					CapabilityChecker: nil,
					ErrorContext:      "test credentials",
				},
			},
			capabilities:         &hyperv1.Capabilities{},
			expectedSecretsCount: 1,
			expectedErrors:       0,
			validateSecret: func(secret *corev1.Secret, config AzureCredentialConfig) {
				// Should not have azure_client_id when ClientID is empty
				g.Expect(secret.Data["azure_client_id"]).To(BeNil())
				g.Expect(secret.Data["azure_region"]).To(Equal([]byte("eastus")))
			},
		},
		{
			name: "handles nil manifest function gracefully",
			configs: []AzureCredentialConfig{
				{
					Name:              "broken-secret",
					ManifestFunc:      func() *corev1.Secret { return nil }, // Returns nil
					ClientID:          "test-client-id",
					CapabilityChecker: nil,
					ErrorContext:      "broken credentials",
				},
			},
			capabilities:         &hyperv1.Capabilities{},
			expectedSecretsCount: 0,
			expectedErrors:       1, // Should get an error for nil manifest
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client for testing
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				Build()

			// Mock createOrUpdate function that simulates secret creation
			var createdSecrets []*corev1.Secret
			mockCreateOrUpdate := func(ctx context.Context, client client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				secret, ok := obj.(*corev1.Secret)
				if !ok {
					return controllerutil.OperationResultNone, fmt.Errorf("expected Secret, got %T", obj)
				}

				// Call the mutate function to set up the secret data
				if err := mutate(); err != nil {
					return controllerutil.OperationResultNone, err
				}

				// Store the secret for validation
				createdSecrets = append(createdSecrets, secret.DeepCopy())
				return controllerutil.OperationResultCreated, nil
			}

			// Call the function under test
			errs := ReconcileAzureCredentials(
				t.Context(),
				fakeClient,
				mockCreateOrUpdate,
				baseSecretData,
				tt.configs,
				tt.capabilities,
			)

			// Validate error count
			g.Expect(len(errs)).To(Equal(tt.expectedErrors))

			// Validate secret count
			g.Expect(len(createdSecrets)).To(Equal(tt.expectedSecretsCount))

			// Validate each created secret
			for i, secret := range createdSecrets {
				if i < len(tt.configs) && tt.validateSecret != nil {
					// Find the corresponding config for this secret
					for _, config := range tt.configs {
						if (config.CapabilityChecker == nil || config.CapabilityChecker(tt.capabilities)) &&
							config.ManifestFunc() != nil &&
							config.ManifestFunc().Name == secret.Name {
							tt.validateSecret(secret, config)
							break
						}
					}
				}
			}
		})
	}
}

// Helper function to check if a capability is disabled
func isCapabilityDisabled(capabilities *hyperv1.Capabilities, capability hyperv1.OptionalCapability) bool {
	if capabilities == nil {
		return false
	}
	for _, disabled := range capabilities.Disabled {
		if disabled == capability {
			return true
		}
	}
	return false
}
