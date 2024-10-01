package azureutil

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// We received this images directly from Microsoft; we should expect them to change as Microsoft continues development on both containers.
// We are scheduled to receive new updates to these containers in October 2024 to support Managed Identities. They currently only support Service Principal.
// TODO past October, will we receive new versions?
const (
	AdapterInitImage   = "aromiwi.azurecr.io/artifact/b8e9ef87-cd63-4085-ab14-1c637806568c/buddy/adapter-init:20240905.9"
	AdapterServerImage = "aromiwi.azurecr.io/artifact/b8e9ef87-cd63-4085-ab14-1c637806568c/buddy/adapter-server:20240905.5"
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

// GetAzureCredentialsFromSecret gets the Service Principal client ID, client secret, and tenant ID from the credentials
// secret. This function will be modified a bit once the Microsoft sidecar containers support Managed Identity are
// delivered (expected Oct 2024).
func GetAzureCredentialsFromSecret(ctx context.Context, c client.Client, namespace, credsName string) (*corev1.Secret, error) {
	var azureCredentials corev1.Secret

	// Retrieve the Azure credentials secret to extract the needed fields for the managed identity containers
	credentialsSecretName := client.ObjectKey{Namespace: namespace, Name: credsName}
	if err := c.Get(ctx, credentialsSecretName, &azureCredentials); err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", credentialsSecretName, err)
	}

	for _, expectedKey := range []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID"} {
		if _, found := azureCredentials.Data[expectedKey]; !found {
			return nil, fmt.Errorf("credentials secret for cluster doesn't have required key %s", expectedKey)
		}
	}

	return &azureCredentials, nil
}

// AdapterInitContainer returns the Microsoft adapter-init init container. This container needs the NET_ADMIN permission
// so the adapter-server sidecar container can intercept the Managed Identity Azure API authentication calls.
func AdapterInitContainer() corev1.Container {
	return corev1.Container{
		Name:            "adapter-init",
		Image:           AdapterInitImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"NET_ADMIN",
				},
			},
		}}
}

// AdapterServerContainer returns the Microsoft adapter-server sidecar container. Currently, this container mimics Azure
// Managed Identity approval and returns an authentication token. The container currently needs a Service Principal to
// do this. Future versions of this container will be able to take a Managed Identity instead.
func AdapterServerContainer(clientID, clientSecret, tenantID string) corev1.Container {
	return corev1.Container{Name: "adapter-server",
		Image:           AdapterServerImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"sp"},
		Env: []corev1.EnvVar{
			{
				Name:  "AZURE_CLIENT_ID",
				Value: clientID,
			},
			{
				Name:  "AZURE_CLIENT_SECRET",
				Value: clientSecret,
			},
			{
				Name:  "AZURE_TENANT_ID",
				Value: tenantID,
			},
		}}
}
