package azure

import (
	"testing"
)

func TestSortResourcesByDeletionOrder(t *testing.T) {
	tests := []struct {
		name          string
		resources     []resourceToDelete
		expectedOrder []string // expected order of resource types
	}{
		{
			name: "When mixed resource types are provided it should sort by deletion priority",
			resources: []resourceToDelete{
				{id: "1", name: "storage", resourceType: "Microsoft.Storage/storageAccounts"},
				{id: "2", name: "vnetlink", resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks"},
				{id: "3", name: "vm", resourceType: "Microsoft.Compute/virtualMachines"},
				{id: "4", name: "lb", resourceType: "Microsoft.Network/loadBalancers"},
				{id: "5", name: "nic", resourceType: "Microsoft.Network/networkInterfaces"},
			},
			expectedOrder: []string{
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks", // 1
				"Microsoft.Compute/virtualMachines",                     // 2
				"Microsoft.Network/networkInterfaces",                   // 3
				"Microsoft.Network/loadBalancers",                       // 4
				"Microsoft.Storage/storageAccounts",                     // 10
			},
		},
		{
			name: "When network resources are provided it should put vnet before dns zones but after nsg",
			resources: []resourceToDelete{
				{id: "1", name: "dns", resourceType: "Microsoft.Network/privateDnsZones"},
				{id: "2", name: "vnet", resourceType: "Microsoft.Network/virtualNetworks"},
				{id: "3", name: "nsg", resourceType: "Microsoft.Network/networkSecurityGroups"},
				{id: "4", name: "identity", resourceType: "Microsoft.ManagedIdentity/userAssignedIdentities"},
			},
			expectedOrder: []string{
				"Microsoft.Network/networkSecurityGroups",          // 7
				"Microsoft.Network/virtualNetworks",                // 8
				"Microsoft.Network/privateDnsZones",                // 9
				"Microsoft.ManagedIdentity/userAssignedIdentities", // 11
			},
		},
		{
			name: "When unknown resource types are provided it should place them after known types",
			resources: []resourceToDelete{
				{id: "1", name: "unknown", resourceType: "Microsoft.Unknown/someResource"},
				{id: "2", name: "vm", resourceType: "Microsoft.Compute/virtualMachines"},
			},
			expectedOrder: []string{
				"Microsoft.Compute/virtualMachines", // 2
				"Microsoft.Unknown/someResource",    // 99
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortResourcesByDeletionOrder(tt.resources)

			for i, expected := range tt.expectedOrder {
				if tt.resources[i].resourceType != expected {
					t.Errorf("position %d: got %s, expected %s", i, tt.resources[i].resourceType, expected)
				}
			}
		})
	}
}

func TestGetAPIVersionForResourceType(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		expected     string
	}{
		{
			name:         "When resource type is public IP addresses it should return correct API version",
			resourceType: "Microsoft.Network/publicIPAddresses",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is load balancers it should return correct API version",
			resourceType: "Microsoft.Network/loadBalancers",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is network interfaces it should return correct API version",
			resourceType: "Microsoft.Network/networkInterfaces",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is network security groups it should return correct API version",
			resourceType: "Microsoft.Network/networkSecurityGroups",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is virtual networks it should return correct API version",
			resourceType: "Microsoft.Network/virtualNetworks",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is private DNS zones it should return correct API version",
			resourceType: "Microsoft.Network/privateDnsZones",
			expected:     "2020-06-01",
		},
		{
			name:         "When resource type is private DNS zone virtual network links it should return correct API version",
			resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
			expected:     "2020-06-01",
		},
		{
			name:         "When resource type is virtual machines it should return correct API version",
			resourceType: "Microsoft.Compute/virtualMachines",
			expected:     "2024-03-01",
		},
		{
			name:         "When resource type is disks it should return correct API version",
			resourceType: "Microsoft.Compute/disks",
			expected:     "2023-10-02",
		},
		{
			name:         "When resource type is storage accounts it should return correct API version",
			resourceType: "Microsoft.Storage/storageAccounts",
			expected:     "2023-01-01",
		},
		{
			name:         "When resource type is user assigned identities it should return correct API version",
			resourceType: "Microsoft.ManagedIdentity/userAssignedIdentities",
			expected:     "2023-01-31",
		},
		{
			name:         "When resource type is unknown it should return default API version",
			resourceType: "Microsoft.Unknown/someResource",
			expected:     "2021-04-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAPIVersionForResourceType(tt.resourceType)
			if result != tt.expected {
				t.Errorf("getAPIVersionForResourceType(%s) = %s, expected %s", tt.resourceType, result, tt.expected)
			}
		})
	}
}

func TestGetResourceGroupName(t *testing.T) {
	tests := []struct {
		name     string
		opts     DestroyInfraOptions
		expected string
	}{
		{
			name: "When custom resource group name is provided it should use that name",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "custom-rg-name",
			},
			expected: "custom-rg-name",
		},
		{
			name: "When no resource group name is provided it should use default format",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "",
			},
			expected: "test-cluster-abc123",
		},
		{
			name: "When empty resource group name is provided it should use default format",
			opts: DestroyInfraOptions{
				Name:              "my-cluster",
				InfraID:           "xyz789",
				ResourceGroupName: "",
			},
			expected: "my-cluster-xyz789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.opts.GetResourceGroupName()
			if result != tt.expected {
				t.Errorf("GetResourceGroupName() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
