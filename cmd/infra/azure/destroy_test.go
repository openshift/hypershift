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

func TestGetResourceGroupName(t *testing.T) {
	tests := []struct {
		name     string
		opts     DestroyInfraOptions
		expected string
	}{
		{
			name: "custom resource group name",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "custom-rg-name",
			},
			expected: "custom-rg-name",
		},
		{
			name: "default resource group name",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "",
			},
			expected: "test-cluster-abc123",
		},
		{
			name: "empty custom resource group name uses default",
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
