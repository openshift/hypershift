package nodepool

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
)

func TestGenerateACRCredentialProviderMachineConfig(t *testing.T) {
	testCases := []struct {
		name           string
		credentials    *hyperv1.AzureImageRegistryCredentials
		cloud          string
		tenantID       string
		subscriptionID string
		expectErr      bool
		expectErrMsg   string
		validate       func(g Gomega, ignCfg *igntypes.Config)
	}{
		{
			name:           "When credentials are nil it should return an error",
			credentials:    nil,
			cloud:          "AzurePublicCloud",
			tenantID:       "test-tenant",
			subscriptionID: "test-sub",
			expectErr:      true,
			expectErrMsg:   "credentials must not be nil",
		},
		{
			name: "When ManagedIdentity ResourceID is empty it should return an error",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{},
			},
			cloud:          "AzurePublicCloud",
			tenantID:       "test-tenant",
			subscriptionID: "test-sub",
			expectErr:      true,
			expectErrMsg:   "credentials.ManagedIdentity.ResourceID must not be empty",
		},
		{
			name: "When tenantID is empty it should return an error",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id",
				},
			},
			cloud:          "AzurePublicCloud",
			tenantID:       "",
			subscriptionID: "test-sub",
			expectErr:      true,
			expectErrMsg:   "tenantID must not be empty",
		},
		{
			name: "When subscriptionID is empty it should return an error",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id",
				},
			},
			cloud:          "AzurePublicCloud",
			tenantID:       "test-tenant",
			subscriptionID: "",
			expectErr:      true,
			expectErrMsg:   "subscriptionID must not be empty",
		},
		{
			name: "When cloud is empty it should return an error",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id",
				},
			},
			cloud:          "",
			tenantID:       "test-tenant",
			subscriptionID: "test-sub",
			expectErr:      true,
			expectErrMsg:   "cloud must not be empty",
		},
		{
			name: "When valid credentials are set it should generate credential provider config with default wildcards",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
					ResourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.ManagedIdentity/userAssignedIdentities/acr-pull-identity",
				},
			},
			cloud:          "AzurePublicCloud",
			tenantID:       "tenant-abc",
			subscriptionID: "sub-123",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				g.Expect(ignCfg.Storage.Files).To(HaveLen(2))

				credProviderFile := ignCfg.Storage.Files[0]
				g.Expect(credProviderFile.Path).To(Equal("/etc/kubernetes/credential-providers/acr-credential-provider.yaml"))
				content := decodeIgnitionFileContent(g, credProviderFile)

				g.Expect(content).To(ContainSubstring(`"*.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.cn"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.de"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.us"`))
				g.Expect(content).To(ContainSubstring("name: acr-credential-provider"))
				g.Expect(content).To(ContainSubstring("apiVersion: credentialprovider.kubelet.k8s.io/v1"))
				g.Expect(content).To(ContainSubstring("defaultCacheDuration: 10m"))
			},
		},
		{
			name: "When generating MachineConfig it should include both config files without systemd drop-in",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
					ResourceID: "/subscriptions/sub-789/resourceGroups/rg-dev/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dev-identity",
				},
			},
			cloud:          "AzurePublicCloud",
			tenantID:       "tenant-dev",
			subscriptionID: "sub-789",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				g.Expect(ignCfg.Storage.Files).To(HaveLen(2))
				g.Expect(ignCfg.Storage.Files[0].Path).To(Equal("/etc/kubernetes/credential-providers/acr-credential-provider.yaml"))
				g.Expect(ignCfg.Storage.Files[1].Path).To(Equal("/etc/kubernetes/acr-azure.json"))

				g.Expect(*ignCfg.Storage.Files[0].Mode).To(Equal(0644))
				g.Expect(*ignCfg.Storage.Files[1].Mode).To(Equal(0600))
				g.Expect(*ignCfg.Storage.Files[0].Overwrite).To(BeTrue())
				g.Expect(*ignCfg.Storage.Files[1].Overwrite).To(BeTrue())
				g.Expect(ignCfg.Systemd.Units).To(BeEmpty())
			},
		},
		{
			name: "When only resourceID is set it should use resourceID in azure.json",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
					ResourceID: "/subscriptions/sub-aaa/resourceGroups/rg-aaa/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-identity",
				},
			},
			cloud:          "AzurePublicCloud",
			tenantID:       "my-tenant-id-12345",
			subscriptionID: "my-subscription-id-67890",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				azureJSONFile := ignCfg.Storage.Files[1]
				content := decodeIgnitionFileContent(g, azureJSONFile)

				var azureCfg acrAzureConfig
				err := json.Unmarshal([]byte(content), &azureCfg)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(azureCfg.Cloud).To(Equal("AzurePublicCloud"))
				g.Expect(azureCfg.TenantID).To(Equal("my-tenant-id-12345"))
				g.Expect(azureCfg.SubscriptionID).To(Equal("my-subscription-id-67890"))
				g.Expect(azureCfg.UseManagedIdentityExtension).To(BeTrue())
				g.Expect(azureCfg.UserAssignedIdentityID).To(Equal(
					"/subscriptions/sub-aaa/resourceGroups/rg-aaa/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-identity",
				))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			mc, err := generateACRCredentialProviderMachineConfig(tc.credentials, tc.cloud, tc.tenantID, tc.subscriptionID)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(mc).ToNot(BeNil())
			g.Expect(mc.Name).To(Equal("50-acr-credential-provider"))
			g.Expect(mc.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			g.Expect(mc.Kind).To(Equal("MachineConfig"))

			ignCfg := &igntypes.Config{}
			err = json.Unmarshal(mc.Spec.Config.Raw, ignCfg)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ignCfg.Ignition.Version).To(Equal("3.2.0"))

			if tc.validate != nil {
				tc.validate(g, ignCfg)
			}
		})
	}
}

func TestGenerateCredentialProviderConfig(t *testing.T) {
	t.Run("When generating credential provider config it should include all default wildcard patterns", func(t *testing.T) {
		g := NewWithT(t)

		content := string(generateCredentialProviderConfig())
		g.Expect(content).To(ContainSubstring("apiVersion: kubelet.config.k8s.io/v1"))
		g.Expect(content).To(ContainSubstring("kind: CredentialProviderConfig"))
		g.Expect(content).To(ContainSubstring(`"*.azurecr.io"`))
		g.Expect(content).To(ContainSubstring(`"*.azurecr.cn"`))
		g.Expect(content).To(ContainSubstring(`"*.azurecr.de"`))
		g.Expect(content).To(ContainSubstring(`"*.azurecr.us"`))
		g.Expect(content).To(ContainSubstring("/etc/kubernetes/acr-azure.json"))
	})
}

func TestGenerateACRAzureJSON(t *testing.T) {
	testCases := []struct {
		name            string
		managedIdentity string
		cloud           string
		tenantID        string
		subscriptionID  string
		validate        func(g Gomega, content string)
	}{
		{
			name:            "When generating azure.json it should produce valid JSON with all fields",
			managedIdentity: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-id",
			cloud:           "AzurePublicCloud",
			tenantID:        "test-tenant",
			subscriptionID:  "test-subscription",
			validate: func(g Gomega, content string) {
				var cfg acrAzureConfig
				err := json.Unmarshal([]byte(content), &cfg)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cfg.Cloud).To(Equal("AzurePublicCloud"))
				g.Expect(cfg.TenantID).To(Equal("test-tenant"))
				g.Expect(cfg.SubscriptionID).To(Equal("test-subscription"))
				g.Expect(cfg.UseManagedIdentityExtension).To(BeTrue())
				g.Expect(cfg.UserAssignedIdentityID).To(Equal(
					"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-id",
				))
			},
		},
		{
			name:            "When cloud is AzureUSGovernmentCloud it should use that cloud value",
			managedIdentity: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/gov-id",
			cloud:           "AzureUSGovernmentCloud",
			tenantID:        "gov-tenant",
			subscriptionID:  "gov-subscription",
			validate: func(g Gomega, content string) {
				var cfg acrAzureConfig
				err := json.Unmarshal([]byte(content), &cfg)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cfg.Cloud).To(Equal("AzureUSGovernmentCloud"))
				g.Expect(cfg.UserAssignedIdentityID).To(Equal(
					"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/gov-id",
				))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := generateACRAzureJSON(tc.managedIdentity, tc.cloud, tc.tenantID, tc.subscriptionID)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.validate != nil {
				tc.validate(g, string(result))
			}
		})
	}
}

// decodeIgnitionFileContent extracts and decodes the base64-encoded content
// from an ignition File's data URI source field.
func decodeIgnitionFileContent(g Gomega, file igntypes.File) string {
	g.Expect(file.Contents.Source).ToNot(BeNil())
	source := *file.Contents.Source

	prefix := "data:text/plain;charset=utf-8;base64,"
	g.Expect(source).To(HavePrefix(prefix))
	encoded := strings.TrimPrefix(source, prefix)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	g.Expect(err).ToNot(HaveOccurred())

	return string(decoded)
}

func TestGetACRCredentialProviderConfig(t *testing.T) {
	testCases := []struct {
		name         string
		nodePool     *hyperv1.NodePool
		hc           *hyperv1.HostedCluster
		expectNil    bool
		expectErr    bool
		expectErrMsg string
		validate     func(g Gomega, cms []corev1.ConfigMap)
	}{
		{
			name: "When platform is not Azure it should return nil",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			hc:        &hyperv1.HostedCluster{},
			expectNil: true,
		},
		{
			name: "When Azure platform has empty ImageRegistryCredentials it should return nil",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type:  hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{},
					},
				},
			},
			hc:        &hyperv1.HostedCluster{},
			expectNil: true,
		},
		{
			name: "When Azure platform spec is nil on HostedCluster it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							ImageRegistryCredentials: hyperv1.AzureImageRegistryCredentials{
								ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
									ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id",
								},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:  hyperv1.AzurePlatform,
						Azure: nil,
					},
				},
			},
			expectErr:    true,
			expectErrMsg: "hostedCluster platform Azure spec must be set",
		},
		{
			name: "When valid credentials are set it should return a ConfigMap with MachineConfig YAML",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							ImageRegistryCredentials: hyperv1.AzureImageRegistryCredentials{
								ManagedIdentity: hyperv1.UserAssignedManagedIdentity{
									ResourceID: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/acr-pull",
								},
							},
						},
					},
				},
			},
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Cloud:          "AzurePublicCloud",
							TenantID:       "test-tenant",
							SubscriptionID: "test-sub",
						},
					},
				},
			},
			validate: func(g Gomega, cms []corev1.ConfigMap) {
				g.Expect(cms).To(HaveLen(1))
				mcYAML, ok := cms[0].Data[TokenSecretConfigKey]
				g.Expect(ok).To(BeTrue(), "ConfigMap should have TokenSecretConfigKey")
				g.Expect(mcYAML).To(ContainSubstring("50-acr-credential-provider"))
				g.Expect(mcYAML).To(ContainSubstring("MachineConfig"))
				g.Expect(mcYAML).To(ContainSubstring("machineconfiguration.openshift.io/role"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			cg := &ConfigGenerator{
				hostedCluster: tc.hc,
				nodePool:      tc.nodePool,
			}

			cms, err := cg.getACRCredentialProviderConfig()
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tc.expectNil {
				g.Expect(cms).To(BeNil())
				return
			}

			if tc.validate != nil {
				tc.validate(g, cms)
			}
		})
	}
}
