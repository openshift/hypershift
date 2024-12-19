package azureutil

import (
	"context"
	"fmt"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

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

	if vnet.Properties.Subnets == nil || len(vnet.Properties.Subnets) == 0 {
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

func GetKeyVaultAuthorizedUser() string {
	return os.Getenv(config.AROHCPKeyVaultManagedIdentityClientID)
}

func CreateEnvVarsForAzureManagedIdentity(azureClientID, azureTenantID, azureCertificateName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  config.ManagedAzureClientIdEnvVarKey,
			Value: azureClientID,
		},
		{
			Name:  config.ManagedAzureTenantIdEnvVarKey,
			Value: azureTenantID,
		},
		{
			Name:  config.ManagedAzureCertificatePathEnvVarKey,
			Value: config.ManagedAzureCertificatePath + azureCertificateName,
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
