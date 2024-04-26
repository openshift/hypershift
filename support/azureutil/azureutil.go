package azureutil

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// GetSubnetNameFromSubnetID extracts the subnet name from a subnet ID
// Example subnet ID: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/virtualNetworks/<vnetName>/subnets/<subnetName>
func GetSubnetNameFromSubnetID(subnetID string) (string, error) {
	subnet, err := arm.ParseResourceID(subnetID)
	if err != nil {
		return "", fmt.Errorf("failed to parse subnet ID %q: %v", subnetID, err)
	}

	if subnet.Name == "" {
		return "", fmt.Errorf("failed to parse subnet name from %q", subnetID)
	}

	return subnet.Name, nil
}

// GetNetworkSecurityGroupNameFromNetworkSecurityGroupID extracts the network security group (nsg) name from a nsg ID
// Example nsg ID: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/networkSecurityGroups/<nsgName>
func GetNetworkSecurityGroupNameFromNetworkSecurityGroupID(nsgID string) (string, error) {
	nsg, err := arm.ParseResourceID(nsgID)
	if err != nil {
		return "", fmt.Errorf("failed to parse network security group ID %q: %v", nsgID, err)
	}

	if nsg.Name == "" {
		return "", fmt.Errorf("failed to parse network security group name from %q", nsgID)
	}

	return nsg.Name, nil
}
