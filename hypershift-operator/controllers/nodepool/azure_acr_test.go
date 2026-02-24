package nodepool

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestGenerateACRCredentialProviderMachineConfig(t *testing.T) {
	testCases := []struct {
		name           string
		credentials    *hyperv1.AzureImageRegistryCredentials
		tenantID       string
		subscriptionID string
		expectErr      bool
		expectErrMsg   string
		validate       func(g Gomega, ignCfg *igntypes.Config)
	}{
		{
			name:           "When credentials are nil it should return an error",
			credentials:    nil,
			tenantID:       "test-tenant",
			subscriptionID: "test-sub",
			expectErr:      true,
			expectErrMsg:   "credentials must not be nil",
		},
		{
			name: "When tenantID is empty it should return an error",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id",
				Registries:      []string{"myregistry.azurecr.io"},
			},
			tenantID:       "",
			subscriptionID: "test-sub",
			expectErr:      true,
			expectErrMsg:   "tenantID must not be empty",
		},
		{
			name: "When subscriptionID is empty it should return an error",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id",
				Registries:      []string{"myregistry.azurecr.io"},
			},
			tenantID:       "test-tenant",
			subscriptionID: "",
			expectErr:      true,
			expectErrMsg:   "subscriptionID must not be empty",
		},
		{
			name: "When credentials have single registry it should generate valid credential provider config",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.ManagedIdentity/userAssignedIdentities/acr-pull-identity",
				Registries:      []string{"myregistry.azurecr.io"},
			},
			tenantID:       "tenant-abc",
			subscriptionID: "sub-123",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				g.Expect(ignCfg.Storage.Files).To(HaveLen(2))

				// Verify credential provider config file
				credProviderFile := ignCfg.Storage.Files[0]
				g.Expect(credProviderFile.Path).To(Equal("/etc/kubernetes/credential-providers/acr-credential-provider.yaml"))
				content := decodeIgnitionFileContent(g, credProviderFile)

				// Verify user registry is present
				g.Expect(content).To(ContainSubstring(`"myregistry.azurecr.io"`))

				// Verify default wildcard patterns are present
				g.Expect(content).To(ContainSubstring(`"*.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.cn"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.de"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.us"`))

				// Verify provider name and API version
				g.Expect(content).To(ContainSubstring("name: acr-credential-provider"))
				g.Expect(content).To(ContainSubstring("apiVersion: credentialprovider.kubelet.k8s.io/v1"))
				g.Expect(content).To(ContainSubstring("defaultCacheDuration: 10m"))

				// Verify user registry appears before wildcard patterns
				userRegIdx := strings.Index(content, `"myregistry.azurecr.io"`)
				wildcardIdx := strings.Index(content, `"*.azurecr.io"`)
				g.Expect(userRegIdx).To(BeNumerically("<", wildcardIdx))
			},
		},
		{
			name: "When credentials have multiple registries it should include all registries plus wildcards",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: "/subscriptions/sub-456/resourceGroups/rg-prod/providers/Microsoft.ManagedIdentity/userAssignedIdentities/prod-acr-identity",
				Registries: []string{
					"registry1.azurecr.io",
					"registry2.azurecr.io",
					"registry3.azurecr.io",
				},
			},
			tenantID:       "tenant-prod",
			subscriptionID: "sub-456",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				credProviderFile := ignCfg.Storage.Files[0]
				content := decodeIgnitionFileContent(g, credProviderFile)

				// Verify all user registries are present
				g.Expect(content).To(ContainSubstring(`"registry1.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"registry2.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"registry3.azurecr.io"`))

				// Verify default wildcard patterns are still present
				g.Expect(content).To(ContainSubstring(`"*.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.cn"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.de"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.us"`))

				// Verify ordering: all user registries appear before wildcards
				lastUserRegIdx := strings.LastIndex(content, `"registry3.azurecr.io"`)
				firstWildcardIdx := strings.Index(content, `"*.azurecr.io"`)
				g.Expect(lastUserRegIdx).To(BeNumerically("<", firstWildcardIdx))
			},
		},
		{
			name: "When generating MachineConfig it should include both config files without systemd drop-in",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: "/subscriptions/sub-789/resourceGroups/rg-dev/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dev-identity",
				Registries:      []string{"dev.azurecr.io"},
			},
			tenantID:       "tenant-dev",
			subscriptionID: "sub-789",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				// Verify two files exist
				g.Expect(ignCfg.Storage.Files).To(HaveLen(2))

				// Verify file paths — credential provider config overrides the MCO-rendered path
				g.Expect(ignCfg.Storage.Files[0].Path).To(Equal("/etc/kubernetes/credential-providers/acr-credential-provider.yaml"))
				g.Expect(ignCfg.Storage.Files[1].Path).To(Equal("/etc/kubernetes/acr-azure.json"))

				// Verify file modes
				g.Expect(ignCfg.Storage.Files[0].Mode).ToNot(BeNil())
				g.Expect(*ignCfg.Storage.Files[0].Mode).To(Equal(0644))
				g.Expect(ignCfg.Storage.Files[1].Mode).ToNot(BeNil())
				g.Expect(*ignCfg.Storage.Files[1].Mode).To(Equal(0644))

				// Verify overwrite is set (required to override MCO-rendered file)
				g.Expect(ignCfg.Storage.Files[0].Overwrite).ToNot(BeNil())
				g.Expect(*ignCfg.Storage.Files[0].Overwrite).To(BeTrue())
				g.Expect(ignCfg.Storage.Files[1].Overwrite).ToNot(BeNil())
				g.Expect(*ignCfg.Storage.Files[1].Overwrite).To(BeTrue())

				// Verify no systemd units — kubelet flags are already set by MCO
				g.Expect(ignCfg.Systemd.Units).To(BeEmpty())
			},
		},
		{
			name: "When generating azure.json it should include correct tenant and subscription IDs",
			credentials: &hyperv1.AzureImageRegistryCredentials{
				ManagedIdentity: "/subscriptions/sub-aaa/resourceGroups/rg-aaa/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-identity",
				Registries:      []string{"testacr.azurecr.io"},
			},
			tenantID:       "my-tenant-id-12345",
			subscriptionID: "my-subscription-id-67890",
			expectErr:      false,
			validate: func(g Gomega, ignCfg *igntypes.Config) {
				// Verify azure.json file content
				azureJSONFile := ignCfg.Storage.Files[1]
				g.Expect(azureJSONFile.Path).To(Equal("/etc/kubernetes/acr-azure.json"))
				content := decodeIgnitionFileContent(g, azureJSONFile)

				// Unmarshal and verify the azure config
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

			mc, err := generateACRCredentialProviderMachineConfig(tc.credentials, tc.tenantID, tc.subscriptionID)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(mc).ToNot(BeNil())

			// Verify MachineConfig metadata
			g.Expect(mc.Name).To(Equal("50-acr-credential-provider"))
			g.Expect(mc.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			g.Expect(mc.Kind).To(Equal("MachineConfig"))

			// Unmarshal ignition config from MachineConfig
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
	testCases := []struct {
		name         string
		registries   []string
		expectErr    bool
		expectErrMsg string
		validate     func(g Gomega, content string)
	}{
		{
			name:         "When registries list is empty it should return an error",
			registries:   []string{},
			expectErr:    true,
			expectErrMsg: "at least one registry must be specified",
		},
		{
			name:       "When a single registry is provided it should appear before wildcards",
			registries: []string{"single.azurecr.io"},
			expectErr:  false,
			validate: func(g Gomega, content string) {
				g.Expect(content).To(ContainSubstring("apiVersion: kubelet.config.k8s.io/v1"))
				g.Expect(content).To(ContainSubstring("kind: CredentialProviderConfig"))
				g.Expect(content).To(ContainSubstring(`"single.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.io"`))

				// Verify args reference to azure.json
				g.Expect(content).To(ContainSubstring("/etc/kubernetes/acr-azure.json"))
			},
		},
		{
			name: "When multiple registries are provided it should include all plus wildcards",
			registries: []string{
				"first.azurecr.io",
				"second.azurecr.io",
			},
			expectErr: false,
			validate: func(g Gomega, content string) {
				g.Expect(content).To(ContainSubstring(`"first.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"second.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.io"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.cn"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.de"`))
				g.Expect(content).To(ContainSubstring(`"*.azurecr.us"`))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := generateCredentialProviderConfig(tc.registries)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			if tc.validate != nil {
				tc.validate(g, string(result))
			}
		})
	}
}

func TestGenerateACRAzureJSON(t *testing.T) {
	testCases := []struct {
		name            string
		managedIdentity string
		tenantID        string
		subscriptionID  string
		validate        func(g Gomega, content string)
	}{
		{
			name:            "When generating azure.json it should produce valid JSON with all fields",
			managedIdentity: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-id",
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := generateACRAzureJSON(tc.managedIdentity, tc.tenantID, tc.subscriptionID)
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

	// Parse the data URI: data:text/plain;charset=utf-8;base64,<encoded>
	prefix := "data:text/plain;charset=utf-8;base64,"
	g.Expect(source).To(HavePrefix(prefix))
	encoded := strings.TrimPrefix(source, prefix)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	g.Expect(err).ToNot(HaveOccurred())

	return string(decoded)
}
