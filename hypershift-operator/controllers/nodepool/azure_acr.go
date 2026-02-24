package nodepool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

const (
	// acrCredentialProviderConfigPath is the path where MCO already renders the
	// credential provider config for Azure. Our MachineConfig overrides this file
	// to point at acr-azure.json with user-assigned managed identity settings,
	// instead of the default cloud.conf.
	acrCredentialProviderConfigPath = "/etc/kubernetes/credential-providers/acr-credential-provider.yaml"
	acrAzureJSONPath                = "/etc/kubernetes/acr-azure.json"
	acrCredentialProviderBinName    = "acr-credential-provider"
	acrMachineConfigName            = "50-acr-credential-provider"
)

// acrDefaultMatchImages lists the default wildcard patterns for Azure Container Registry
// endpoints across all Azure clouds. These patterns are always appended to the user-specified
// registries to ensure the credential provider covers all standard ACR endpoints.
var acrDefaultMatchImages = []string{
	"*.azurecr.io",
	"*.azurecr.cn",
	"*.azurecr.de",
	"*.azurecr.us",
}

// acrAzureConfig represents the Azure authentication configuration consumed by the
// acr-credential-provider binary to obtain tokens via the Azure Instance Metadata Service (IMDS).
type acrAzureConfig struct {
	Cloud                       string `json:"cloud"`
	TenantID                    string `json:"tenantId"`
	SubscriptionID              string `json:"subscriptionId"`
	UseManagedIdentityExtension bool   `json:"useManagedIdentityExtension"`
	UserAssignedIdentityID      string `json:"userAssignedIdentityID"`
}

// generateACRCredentialProviderMachineConfig generates a MachineConfig that configures
// the kubelet credential provider for ACR authentication using a managed identity.
// It overrides the MCO-rendered credential provider config at
// /etc/kubernetes/credential-providers/acr-credential-provider.yaml to point at a
// separate acr-azure.json containing the user-assigned managed identity settings.
// The kubelet flags (--image-credential-provider-config and --image-credential-provider-bin-dir)
// are already set by MCO and do not need to be configured here.
func generateACRCredentialProviderMachineConfig(
	credentials *hyperv1.AzureImageRegistryCredentials,
	tenantID string,
	subscriptionID string,
) (*mcfgv1.MachineConfig, error) {
	if credentials == nil {
		return nil, fmt.Errorf("credentials must not be nil")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("tenantID must not be empty")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscriptionID must not be empty")
	}

	credentialProviderConfig, err := generateCredentialProviderConfig(credentials.Registries)
	if err != nil {
		return nil, fmt.Errorf("failed to generate credential provider config: %w", err)
	}

	azureJSON, err := generateACRAzureJSON(credentials.ManagedIdentity, tenantID, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate azure.json: %w", err)
	}

	ignConfig, err := buildACRIgnitionConfig(credentialProviderConfig, azureJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to build ignition config: %w", err)
	}

	mc := &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: acrMachineConfigName,
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": "worker",
			},
		},
		Spec: mcfgv1.MachineConfigSpec{
			Config: runtime.RawExtension{
				Raw: ignConfig,
			},
		},
	}

	return mc, nil
}

// generateCredentialProviderConfig produces the YAML content for the kubelet
// CredentialProviderConfig that configures the acr-credential-provider plugin.
// User-specified registries are listed first, followed by the default wildcard patterns.
func generateCredentialProviderConfig(registries []string) ([]byte, error) {
	if len(registries) == 0 {
		return nil, fmt.Errorf("at least one registry must be specified")
	}

	// Build the matchImages list: user registries first, then default wildcards.
	// Each entry is quoted and indented for YAML formatting.
	var matchImages []string
	for _, reg := range registries {
		matchImages = append(matchImages, fmt.Sprintf("  - %q", reg))
	}
	for _, pattern := range acrDefaultMatchImages {
		matchImages = append(matchImages, fmt.Sprintf("  - %q", pattern))
	}

	config := fmt.Sprintf(`apiVersion: kubelet.config.k8s.io/v1
kind: CredentialProviderConfig
providers:
- name: %s
  apiVersion: credentialprovider.kubelet.k8s.io/v1
  defaultCacheDuration: 10m
  matchImages:
%s
  args:
  - %s
`, acrCredentialProviderBinName, strings.Join(matchImages, "\n"), acrAzureJSONPath)

	return []byte(config), nil
}

// generateACRAzureJSON produces the JSON content for the Azure authentication config
// consumed by the acr-credential-provider binary.
func generateACRAzureJSON(managedIdentity, tenantID, subscriptionID string) ([]byte, error) {
	cfg := acrAzureConfig{
		Cloud:                       "AzurePublicCloud",
		TenantID:                    tenantID,
		SubscriptionID:              subscriptionID,
		UseManagedIdentityExtension: true,
		UserAssignedIdentityID:      managedIdentity,
	}

	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal azure config: %w", err)
	}
	return data, nil
}

// buildACRIgnitionConfig assembles the ignition v3.2.0 Config containing the
// credential provider config file and azure.json file.
func buildACRIgnitionConfig(credentialProviderConfig, azureJSON []byte) ([]byte, error) {
	fileMode := 0644

	ignConfig := igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: "3.2.0",
		},
		Storage: igntypes.Storage{
			Files: []igntypes.File{
				{
					Node: igntypes.Node{
						Path:      acrCredentialProviderConfigPath,
						Overwrite: ptr.To(true),
					},
					FileEmbedded1: igntypes.FileEmbedded1{
						Mode: &fileMode,
						Contents: igntypes.Resource{
							Source: ptr.To(dataURI(credentialProviderConfig)),
						},
					},
				},
				{
					Node: igntypes.Node{
						Path:      acrAzureJSONPath,
						Overwrite: ptr.To(true),
					},
					FileEmbedded1: igntypes.FileEmbedded1{
						Mode: &fileMode,
						Contents: igntypes.Resource{
							Source: ptr.To(dataURI(azureJSON)),
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(ignConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ignition config: %w", err)
	}
	return data, nil
}

// dataURI encodes raw bytes as a base64 data URI suitable for ignition file contents.
func dataURI(data []byte) string {
	return fmt.Sprintf("data:text/plain;charset=utf-8;base64,%s", base64.StdEncoding.EncodeToString(data))
}
