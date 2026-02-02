package azure

import (
	"context"
	"testing"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/go-logr/logr"
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

func TestDestroyInfraRun(t *testing.T) {
	tests := []struct {
		name        string
		transport   policy.Transporter
		expectError bool
	}{
		{
			name:        "When resource group exists it should delete successfully",
			transport:   testutil.NewAzureResourceGroupSuccessTransport(),
			expectError: false,
		},
		{
			name:        "When resource group is not found (404) it should succeed without error",
			transport:   testutil.NewAzureResourceGroupNotFoundTransport(),
			expectError: false,
		},
		{
			name:        "When Azure returns authorization error (403) it should return an error",
			transport:   testutil.NewAzureForbiddenTransport(),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "test-rg",
				Cloud:             "AzurePublicCloud",
				Credentials: &util.AzureCreds{
					SubscriptionID: "test-subscription-id",
				},
				azureCredential: &testutil.FakeAzureCredential{},
				clientOptions: &arm.ClientOptions{
					ClientOptions: azcore.ClientOptions{
						Cloud:     cloud.AzurePublic,
						Transport: tt.transport,
					},
				},
			}

			err := opts.Run(context.Background(), logr.Discard())

			if tt.expectError && err == nil {
				t.Error("Expected an error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
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
			name:         "When given publicIPAddresses it should return 2023-11-01",
			resourceType: "Microsoft.Network/publicIPAddresses",
			expected:     "2023-11-01",
		},
		{
			name:         "When given loadBalancers it should return 2023-11-01",
			resourceType: "Microsoft.Network/loadBalancers",
			expected:     "2023-11-01",
		},
		{
			name:         "When given networkInterfaces it should return 2023-11-01",
			resourceType: "Microsoft.Network/networkInterfaces",
			expected:     "2023-11-01",
		},
		{
			name:         "When given networkSecurityGroups it should return 2023-11-01",
			resourceType: "Microsoft.Network/networkSecurityGroups",
			expected:     "2023-11-01",
		},
		{
			name:         "When given virtualNetworks it should return 2023-11-01",
			resourceType: "Microsoft.Network/virtualNetworks",
			expected:     "2023-11-01",
		},
		{
			name:         "When given privateDnsZones it should return 2020-06-01",
			resourceType: "Microsoft.Network/privateDnsZones",
			expected:     "2020-06-01",
		},
		{
			name:         "When given privateDnsZones virtualNetworkLinks it should return 2020-06-01",
			resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
			expected:     "2020-06-01",
		},
		{
			name:         "When given virtualMachines it should return 2024-03-01",
			resourceType: "Microsoft.Compute/virtualMachines",
			expected:     "2024-03-01",
		},
		{
			name:         "When given disks it should return 2023-10-02",
			resourceType: "Microsoft.Compute/disks",
			expected:     "2023-10-02",
		},
		{
			name:         "When given storageAccounts it should return 2023-01-01",
			resourceType: "Microsoft.Storage/storageAccounts",
			expected:     "2023-01-01",
		},
		{
			name:         "When given userAssignedIdentities it should return 2023-01-31",
			resourceType: "Microsoft.ManagedIdentity/userAssignedIdentities",
			expected:     "2023-01-31",
		},
		{
			name:         "When given unknown resource type it should return default 2021-04-01",
			resourceType: "Microsoft.Unknown/unknownResource",
			expected:     "2021-04-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAPIVersionForResourceType(tt.resourceType)
			if result != tt.expected {
				t.Errorf("getAPIVersionForResourceType(%q) = %q, want %q", tt.resourceType, result, tt.expected)
			}
		})
	}
}

func TestSortResourcesByDeletionOrder(t *testing.T) {
	tests := []struct {
		name     string
		input    []resourceToDelete
		expected []string // expected order of resource types
	}{
		{
			name:     "When given empty list it should not panic",
			input:    []resourceToDelete{},
			expected: []string{},
		},
		{
			name: "When given single resource it should remain unchanged",
			input: []resourceToDelete{
				{resourceType: "Microsoft.Compute/virtualMachines", name: "vm1"},
			},
			expected: []string{"Microsoft.Compute/virtualMachines"},
		},
		{
			name: "When given full dependency chain it should order correctly",
			input: []resourceToDelete{
				{resourceType: "Microsoft.ManagedIdentity/userAssignedIdentities", name: "identity1"},
				{resourceType: "Microsoft.Storage/storageAccounts", name: "storage1"},
				{resourceType: "Microsoft.Network/privateDnsZones", name: "dns1"},
				{resourceType: "Microsoft.Network/virtualNetworks", name: "vnet1"},
				{resourceType: "Microsoft.Network/networkSecurityGroups", name: "nsg1"},
				{resourceType: "Microsoft.Compute/disks", name: "disk1"},
				{resourceType: "Microsoft.Network/publicIPAddresses", name: "pip1"},
				{resourceType: "Microsoft.Network/loadBalancers", name: "lb1"},
				{resourceType: "Microsoft.Network/networkInterfaces", name: "nic1"},
				{resourceType: "Microsoft.Compute/virtualMachines", name: "vm1"},
				{resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks", name: "link1"},
			},
			expected: []string{
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks",
				"Microsoft.Compute/virtualMachines",
				"Microsoft.Network/networkInterfaces",
				"Microsoft.Network/loadBalancers",
				"Microsoft.Network/publicIPAddresses",
				"Microsoft.Compute/disks",
				"Microsoft.Network/networkSecurityGroups",
				"Microsoft.Network/virtualNetworks",
				"Microsoft.Network/privateDnsZones",
				"Microsoft.Storage/storageAccounts",
				"Microsoft.ManagedIdentity/userAssignedIdentities",
			},
		},
		{
			name: "When given unknown resource types it should sort them last with priority 99",
			input: []resourceToDelete{
				{resourceType: "Microsoft.Unknown/customResource", name: "custom1"},
				{resourceType: "Microsoft.Compute/virtualMachines", name: "vm1"},
				{resourceType: "Microsoft.SomeOther/unknownType", name: "unknown1"},
			},
			expected: []string{
				"Microsoft.Compute/virtualMachines",
				"Microsoft.Unknown/customResource",
				"Microsoft.SomeOther/unknownType",
			},
		},
		{
			name: "When given multiple resources of same type it should preserve relative order",
			input: []resourceToDelete{
				{resourceType: "Microsoft.Compute/virtualMachines", name: "vm1"},
				{resourceType: "Microsoft.Compute/virtualMachines", name: "vm2"},
				{resourceType: "Microsoft.Compute/virtualMachines", name: "vm3"},
			},
			expected: []string{
				"Microsoft.Compute/virtualMachines",
				"Microsoft.Compute/virtualMachines",
				"Microsoft.Compute/virtualMachines",
			},
		},
		{
			name: "When given mixed known and unknown types it should sort known first by priority",
			input: []resourceToDelete{
				{resourceType: "Microsoft.Custom/resource", name: "custom1"},
				{resourceType: "Microsoft.Network/networkInterfaces", name: "nic1"},
				{resourceType: "Microsoft.Another/unknown", name: "unknown1"},
				{resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks", name: "link1"},
			},
			expected: []string{
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks",
				"Microsoft.Network/networkInterfaces",
				"Microsoft.Custom/resource",
				"Microsoft.Another/unknown",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortResourcesByDeletionOrder(tt.input)

			if len(tt.input) != len(tt.expected) {
				t.Fatalf("length mismatch: got %d, expected %d", len(tt.input), len(tt.expected))
			}

			for i, resource := range tt.input {
				if resource.resourceType != tt.expected[i] {
					t.Errorf("position %d: got resourceType %q, expected %q", i, resource.resourceType, tt.expected[i])
				}
			}
		})
	}
}
