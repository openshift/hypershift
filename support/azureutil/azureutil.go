package azureutil

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AzureEncryptionKey represents the information needed to access an encryption key in Azure Key Vault
// This information comes from the encryption key ID, which is in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>
type AzureEncryptionKey struct {
	KeyVaultName string
	KeyName      string
	KeyVersion   string
}

// GetAzureCloudConfiguration converts a cloud name string to the Azure SDK cloud.Configuration.
// This function maps the cloud names used in the HyperShift API to the corresponding Azure SDK cloud configurations.
// Valid cloud names are: AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud, and empty string (defaults to AzurePublicCloud).
// Returns an error if the cloud name is not recognized.
func GetAzureCloudConfiguration(cloudName string) (cloud.Configuration, error) {
	switch cloudName {
	case "AzurePublicCloud", "":
		return cloud.AzurePublic, nil
	case "AzureUSGovernmentCloud":
		return cloud.AzureGovernment, nil
	case "AzureChinaCloud":
		return cloud.AzureChina, nil
	default:
		return cloud.Configuration{}, fmt.Errorf("unknown Azure cloud: %s", cloudName)
	}
}

// GetSubnetNameFromSubnetID extracts the subnet name from a subnet ID
// Example subnet ID: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/virtualNetworks/<vnetName>/subnets/<subnetName>
func GetSubnetNameFromSubnetID(subnetID string) (string, error) {
	subnet, err := arm.ParseResourceID(subnetID)
	if err != nil {
		return "", fmt.Errorf("failed to parse subnet ID %q: %v", subnetID, err)
	}

	if !strings.EqualFold(subnet.ResourceType.Type, "virtualnetworks/subnets") {
		return "", fmt.Errorf("invalid resource type '%s', expected 'virtualNetworks/subnets'", subnet.ResourceType.Type)
	}

	if subnet.Name == "" {
		return "", fmt.Errorf("failed to parse subnet name from %q", subnetID)
	}

	return subnet.Name, nil
}

// GetNameAndResourceGroupFromNetworkSecurityGroupID extracts the network security group (nsg) name and its resourrce group name from a nsg ID
// Example nsg ID: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/networkSecurityGroups/<nsgName>
func GetNameAndResourceGroupFromNetworkSecurityGroupID(nsgID string) (string, string, error) {
	nsg, err := arm.ParseResourceID(nsgID)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse network security group ID %q: %v", nsgID, err)
	}

	if !strings.EqualFold(nsg.ResourceType.Type, "networkSecurityGroups") {
		return "", "", fmt.Errorf("invalid resource type '%s', expected 'networkSecurityGroups'", nsg.ResourceType.Type)
	}

	if nsg.Name == "" {
		return "", "", fmt.Errorf("failed to parse network security group name from %q", nsgID)
	}

	if nsg.ResourceGroupName == "" {
		return "", "", fmt.Errorf("failed to parse resource group name from %q", nsgID)
	}

	return nsg.Name, nsg.ResourceGroupName, nil
}

// GetVnetNameAndResourceGroupFromVnetID extracts the VNET name and its resource group from a VNET ID
// Example VNET ID: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/virtualNetworks/<vnetName>
func GetVnetNameAndResourceGroupFromVnetID(vnetID string) (string, string, error) {
	vnet, err := arm.ParseResourceID(vnetID)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse vnet ID %q: %v", vnetID, err)
	}

	if !strings.EqualFold(vnet.ResourceType.Type, "virtualNetworks") {
		return "", "", fmt.Errorf("invalid resource type '%s', expected 'virtualNetworks'", vnet.ResourceType.Type)
	}

	if vnet.Name == "" {
		return "", "", fmt.Errorf("failed to parse vnet name from %q", vnetID)
	}

	if vnet.ResourceGroupName == "" {
		return "", "", fmt.Errorf("failed to parse vnet resource group name from %q", vnetID)
	}

	return vnet.Name, vnet.ResourceGroupName, nil
}

// GetVnetInfoFromVnetID extracts the full information on a VNET from a VNET ID by first getting the VNET name and
// its resource group's name and then using those parameters to query the full information on the VNET using Azure's SDK
func GetVnetInfoFromVnetID(ctx context.Context, vnetID string, subscriptionID string, azureCreds azcore.TokenCredential) (armnetwork.VirtualNetworksClientGetResponse, error) {
	partialVnetInfo, err := arm.ParseResourceID(vnetID)
	if err != nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("failed to parse vnet information from vnet ID %q: %v", vnetID, err)
	}

	if !strings.EqualFold(partialVnetInfo.ResourceType.Type, "virtualNetworks") {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("invalid resource type '%s', expected 'virtualNetworks'", partialVnetInfo.ResourceType.Type)
	}

	if partialVnetInfo.Name == "" {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("failed to parse vnet name from %q", vnetID)
	}

	if partialVnetInfo.ResourceGroupName == "" {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("failed to parse vnet resource group name from %q", vnetID)
	}

	vnet, err := getFullVnetInfo(ctx, subscriptionID, partialVnetInfo.ResourceGroupName, partialVnetInfo.Name, azureCreds)
	if err != nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, err
	}

	return vnet, nil
}

// getFullVnetInfo gets the full information on a VNET
func getFullVnetInfo(ctx context.Context, subscriptionID string, vnetResourceGroupName string, clientVnetName string, azureCreds azcore.TokenCredential) (armnetwork.VirtualNetworksClientGetResponse, error) {
	networksClient, err := armnetwork.NewVirtualNetworksClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("failed to create new virtual networks client: %w", err)
	}

	vnet, err := networksClient.Get(ctx, vnetResourceGroupName, clientVnetName, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("failed to get virtual network: %w", err)
	}

	if vnet.ID == nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("virtual network has no ID")
	}

	if vnet.Name == nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("virtual network has no name")
	}

	if len(vnet.Properties.Subnets) == 0 {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("no subnets found for resource group '%s'", vnetResourceGroupName)
	}

	if vnet.Properties.Subnets[0].ID == nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("no subnet ID found for resource group '%s'", vnetResourceGroupName)
	}

	if vnet.Properties.Subnets[0].Name == nil {
		return armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("no subnet name found for resource group '%s'", vnetResourceGroupName)
	}

	return vnet, nil
}

// GetNetworkSecurityGroupInfo gets the full information on a network security group based on its ID
func GetNetworkSecurityGroupInfo(ctx context.Context, nsgID string, subscriptionID string, azureCreds azcore.TokenCredential) (armnetwork.SecurityGroupsClientGetResponse, error) {
	partialNSGInfo, err := arm.ParseResourceID(nsgID)
	if err != nil {
		return armnetwork.SecurityGroupsClientGetResponse{}, fmt.Errorf("failed to parse network security group id %q: %v", nsgID, err)
	}

	securityGroupClient, err := armnetwork.NewSecurityGroupsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return armnetwork.SecurityGroupsClientGetResponse{}, fmt.Errorf("failed to create security group client: %w", err)
	}

	nsg, err := securityGroupClient.Get(ctx, partialNSGInfo.ResourceGroupName, partialNSGInfo.Name, nil)
	if err != nil {
		return armnetwork.SecurityGroupsClientGetResponse{}, fmt.Errorf("failed to get network security group: %w", err)
	}

	return nsg, nil
}

// GetResourceGroupInfo gets the full information on a resource group based on its name
func GetResourceGroupInfo(ctx context.Context, rgName string, subscriptionID string, azureCreds azcore.TokenCredential) (armresources.ResourceGroupsClientGetResponse, error) {
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return armresources.ResourceGroupsClientGetResponse{}, fmt.Errorf("failed to create new resource groups client: %w", err)
	}

	rg, err := resourceGroupClient.Get(ctx, rgName, nil)
	if err != nil {
		return armresources.ResourceGroupsClientGetResponse{}, fmt.Errorf("failed to get resource group name, '%s': %w", rgName, err)
	}

	return rg, nil
}

// IsAroHCP returns true if the managed service environment variable is set to ARO-HCP
func IsAroHCP() bool {
	return os.Getenv("MANAGED_SERVICE") == hyperv1.AroHCP
}

// IsSelfManagedAzure returns true when the platform is Azure and the managed service is not ARO-HCP
func IsSelfManagedAzure(platform hyperv1.PlatformType) bool {
	return platform == hyperv1.AzurePlatform && !IsAroHCP()
}

// SetAsAroHCPTest sets the proper environment variable for the test, designating this is an ARO-HCP environment
func SetAsAroHCPTest(t *testing.T) {
	t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
}

func GetKeyVaultAuthorizedUser() string {
	return os.Getenv(config.AROHCPKeyVaultManagedIdentityClientID)
}

func CreateEnvVarsForAzureManagedIdentity(azureCredentialsName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  config.ManagedAzureCredentialsFilePath,
			Value: config.ManagedAzureCertificatePath + azureCredentialsName,
		},
	}
}

func CreateVolumeMountForAzureSecretStoreProviderClass(secretStoreVolumeName string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      secretStoreVolumeName,
		MountPath: config.ManagedAzureCertificateMountPath,
		ReadOnly:  true,
	}
}

func CreateVolumeMountForKMSAzureSecretStoreProviderClass(secretStoreVolumeName string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      secretStoreVolumeName,
		MountPath: config.ManagedAzureCredentialsMountPathForKMS,
		ReadOnly:  true,
	}
}

func CreateVolumeForAzureSecretStoreProviderClass(secretStoreVolumeName, secretProviderClassName string) corev1.Volume {
	return corev1.Volume{
		Name: secretStoreVolumeName,
		VolumeSource: corev1.VolumeSource{
			CSI: &corev1.CSIVolumeSource{
				Driver:   config.ManagedAzureSecretsStoreCSIDriver,
				ReadOnly: ptr.To(true),
				VolumeAttributes: map[string]string{
					config.ManagedAzureSecretProviderClass: secretProviderClassName,
				},
			},
		},
	}
}

func GetServicePrincipalScopes(subscriptionID, managedResourceGroupName, nsgResourceGroupName, vnetResourceGroupName, dnsZoneResourceGroupName, component string, assignCustomHCPRoles bool) (string, []string) {
	managedRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedResourceGroupName)
	nsgRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, nsgResourceGroupName)
	vnetRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, vnetResourceGroupName)
	dnsZoneRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, dnsZoneResourceGroupName)

	// Default to the Contributor role
	role := config.ContributorRoleDefinitionID

	scopes := []string{managedRG}

	// TODO CNTRLPLANE-171: CPO, KMS, and NodePoolManagement will need new roles that do not exist today
	switch component {
	case config.CloudProvider:
		role = config.CloudProviderRoleDefinitionID
		scopes = append(scopes, nsgRG, vnetRG)
	case config.Ingress:
		role = config.IngressRoleDefinitionID
		scopes = append(scopes, vnetRG, dnsZoneRG)
	case config.CPO:
		scopes = append(scopes, nsgRG, vnetRG)
		if assignCustomHCPRoles {
			role = config.CPOCustomRoleDefinitionID
		}
	case config.AzureFile:
		role = config.AzureFileRoleDefinitionID
		scopes = append(scopes, nsgRG, vnetRG)
	case config.AzureDisk:
		role = config.AzureDiskRoleDefinitionID
	case config.CNCC:
		scopes = append(scopes, vnetRG)
		role = config.NetworkRoleDefinitionID
	case config.CIRO:
		role = config.ImageRegistryRoleDefinitionID
	case config.NodePoolMgmt:
		scopes = append(scopes, vnetRG)
		if assignCustomHCPRoles {
			role = config.CAPZCustomRoleDefinitionID
		}
	}

	return role, scopes
}

// GetKeyVaultDNSSuffixFromCloudType simply mimics the functionality in environments.go from the Azure SDK, github.com/Azure/go-autorest.
// This function is used to get the DNS suffix for the Key Vault based on the cloud type.
func GetKeyVaultDNSSuffixFromCloudType(cloud string) (string, error) {
	cloud = strings.ToUpper(cloud)

	switch cloud {
	case "AZURECHINACLOUD":
		return "vault.azure.cn", nil
	case "AZURECLOUD":
		return "vault.azure.net", nil
	case "AZUREPUBLICCLOUD":
		return "vault.azure.net", nil
	case "AZUREUSGOVERNMENT":
		return "vault.usgovcloudapi.net", nil
	case "AZUREUSGOVERNMENTCLOUD":
		return "vault.usgovcloudapi.net", nil
	default:
		return "", fmt.Errorf("unknown cloud type %q", cloud)
	}
}

// GetAzureEncryptionKeyInfo extracts the key vault name, key name, and key version from an encryption key ID
// The encryption key ID is in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>
func GetAzureEncryptionKeyInfo(encryptionKeyID string) (*AzureEncryptionKey, error) {
	parsed, err := url.Parse(encryptionKeyID)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key identifier %q: %w", encryptionKeyID, err)
	}

	// Ensure the host is present
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("invalid encryption key identifier %q: missing host", encryptionKeyID)

	}

	// Ensure the path starts with /keys/
	if !strings.HasPrefix(parsed.Path, "/keys/") {
		return nil, fmt.Errorf("invalid encryption key identifier %q: expected path to start with /keys/", encryptionKeyID)
	}

	// Ensure the path has exactly two parts
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/keys/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid encryption key identifier %q: expected /keys/<keyName>/<keyVersion>", encryptionKeyID)
	}

	// Ensure the vault name is present
	vaultName := strings.Split(host, ".")[0]
	if vaultName == "" {
		return nil, fmt.Errorf("invalid encryption key identifier %q: could not derive vault name from host %q", encryptionKeyID, host)
	}

	return &AzureEncryptionKey{
		KeyVaultName: vaultName,
		KeyName:      parts[0],
		KeyVersion:   parts[1],
	}, nil
}

// AzureCredentialConfig defines the configuration for creating an Azure credential secret
type AzureCredentialConfig struct {
	Name              string
	ManifestFunc      func() *corev1.Secret
	ClientID          string
	CapabilityChecker func(*hyperv1.Capabilities) bool
	ErrorContext      string
}

// ReconcileAzureCredentials creates or updates Azure credential secrets based on the provided configurations
func ReconcileAzureCredentials(
	ctx context.Context,
	client client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	baseSecretData map[string][]byte,
	configs []AzureCredentialConfig,
	capabilities *hyperv1.Capabilities,
) []error {
	var errors []error

	for _, config := range configs {
		// Skip credentials that don't meet capability requirements
		if config.CapabilityChecker != nil && !config.CapabilityChecker(capabilities) {
			continue
		}

		// Get the secret manifest
		secret := config.ManifestFunc()
		if secret == nil {
			errors = append(errors, fmt.Errorf("failed to get secret manifest for %s", config.Name))
			continue
		}

		// Create or update the secret
		if _, err := createOrUpdate(ctx, client, secret, func() error {
			// Clone base secret data to avoid mutation
			secretData := maps.Clone(baseSecretData)

			// Add the client ID if provided
			if config.ClientID != "" {
				secretData["azure_client_id"] = []byte(config.ClientID)
			}

			secret.Data = secretData
			return nil
		}); err != nil {
			errorMsg := fmt.Sprintf("failed to reconcile %s", config.ErrorContext)
			if config.ErrorContext == "" {
				errorMsg = fmt.Sprintf("failed to reconcile %s credentials", config.Name)
			}
			errors = append(errors, fmt.Errorf("%s: %w", errorMsg, err))
		}
	}

	return errors
}
