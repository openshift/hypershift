package v1beta1

import (
	"encoding/json"
	"testing"
)

// azurePlatformSpecNMinus1 represents the previous version of AzurePlatformSpec
// before the OutboundType field was added. Used to verify N-1/N+1 serialization
// compatibility for consumers that vendor this module and serialize to storage
// outside CRD validation (e.g., ARO-HCP Cosmos DB).
type azurePlatformSpecNMinus1 struct {
	Cloud                     string                           `json:"cloud,omitempty"`            //nolint:kubeapilinter // test-only N-1 compat struct
	Location                  string                           `json:"location"`                   //nolint:kubeapilinter // test-only N-1 compat struct
	ResourceGroupName         string                           `json:"resourceGroup"`              //nolint:kubeapilinter // test-only N-1 compat struct
	VnetID                    string                           `json:"vnetID"`                     //nolint:kubeapilinter // test-only N-1 compat struct
	SubnetID                  string                           `json:"subnetID"`                   //nolint:kubeapilinter // test-only N-1 compat struct
	SubscriptionID            string                           `json:"subscriptionID"`             //nolint:kubeapilinter // test-only N-1 compat struct
	SecurityGroupID           string                           `json:"securityGroupID"`            //nolint:kubeapilinter // test-only N-1 compat struct
	AzureAuthenticationConfig AzureAuthenticationConfiguration `json:"azureAuthenticationConfig"`  //nolint:kubeapilinter // test-only N-1 compat struct
	TenantID                  string                           `json:"tenantID"`                   //nolint:kubeapilinter // test-only N-1 compat struct
	ContainerRegistry         AzureContainerRegistryConfig     `json:"containerRegistry,omitzero"` //nolint:kubeapilinter // test-only N-1 compat struct
	Topology                  AzureTopologyType                `json:"topology,omitempty"`         //nolint:kubeapilinter // test-only N-1 compat struct
	Private                   AzurePrivateSpec                 `json:"private,omitzero"`           //nolint:kubeapilinter // test-only N-1 compat struct
	// OutboundType intentionally absent — this is the N-1 struct.
}

func TestAzurePlatformSpecOutboundTypeSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       AzurePlatformSpec
		expectedJSON  string
		nMinus1Result azurePlatformSpecNMinus1
	}{
		{
			name: "When OutboundType is omitted it should match N-1 JSON shape exactly",
			current: AzurePlatformSpec{
				Location:          "eastus",
				ResourceGroupName: "test-rg",
				VnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
				SubnetID:          "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				SubscriptionID:    "sub-123",
				SecurityGroupID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
				TenantID:          "tenant-456",
			},
			expectedJSON: `{"location":"eastus","resourceGroup":"test-rg","vnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet","subnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet","subscriptionID":"sub-123","securityGroupID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg","azureAuthenticationConfig":{"azureAuthenticationConfigType":""},"tenantID":"tenant-456"}`,
			nMinus1Result: azurePlatformSpecNMinus1{
				Location:          "eastus",
				ResourceGroupName: "test-rg",
				VnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
				SubnetID:          "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				SubscriptionID:    "sub-123",
				SecurityGroupID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
				TenantID:          "tenant-456",
			},
		},
		{
			name: "When OutboundType is LoadBalancer it should include the field and N-1 should ignore it",
			current: AzurePlatformSpec{
				Location:          "eastus",
				ResourceGroupName: "test-rg",
				VnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
				SubnetID:          "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				SubscriptionID:    "sub-123",
				SecurityGroupID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
				TenantID:          "tenant-456",
				OutboundType:      AzureOutboundTypeLoadBalancer,
			},
			expectedJSON: `{"location":"eastus","resourceGroup":"test-rg","vnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet","subnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet","subscriptionID":"sub-123","securityGroupID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg","azureAuthenticationConfig":{"azureAuthenticationConfigType":""},"tenantID":"tenant-456","outboundType":"LoadBalancer"}`,
			nMinus1Result: azurePlatformSpecNMinus1{
				Location:          "eastus",
				ResourceGroupName: "test-rg",
				VnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
				SubnetID:          "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				SubscriptionID:    "sub-123",
				SecurityGroupID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
				TenantID:          "tenant-456",
			},
		},
		{
			name: "When OutboundType is UserDefinedRouting it should include the field and N-1 should ignore it",
			current: AzurePlatformSpec{
				Location:          "eastus",
				ResourceGroupName: "test-rg",
				VnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
				SubnetID:          "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				SubscriptionID:    "sub-123",
				SecurityGroupID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
				TenantID:          "tenant-456",
				OutboundType:      AzureOutboundTypeUserDefinedRouting,
			},
			expectedJSON: `{"location":"eastus","resourceGroup":"test-rg","vnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet","subnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet","subscriptionID":"sub-123","securityGroupID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg","azureAuthenticationConfig":{"azureAuthenticationConfigType":""},"tenantID":"tenant-456","outboundType":"UserDefinedRouting"}`,
			nMinus1Result: azurePlatformSpecNMinus1{
				Location:          "eastus",
				ResourceGroupName: "test-rg",
				VnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
				SubnetID:          "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				SubscriptionID:    "sub-123",
				SecurityGroupID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
				TenantID:          "tenant-456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal current (N) version
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output:\n  got:  %s\n  want: %s", string(data), tt.expectedJSON)
			}

			// Deserialize into N-1 struct (simulates rollback code reading new data)
			var nMinus1 azurePlatformSpecNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1.Location != tt.nMinus1Result.Location {
				t.Errorf("N-1 Location mismatch: got %s, want %s", nMinus1.Location, tt.nMinus1Result.Location)
			}
			if nMinus1.ResourceGroupName != tt.nMinus1Result.ResourceGroupName {
				t.Errorf("N-1 ResourceGroupName mismatch: got %s, want %s", nMinus1.ResourceGroupName, tt.nMinus1Result.ResourceGroupName)
			}
			if nMinus1.SubscriptionID != tt.nMinus1Result.SubscriptionID {
				t.Errorf("N-1 SubscriptionID mismatch: got %s, want %s", nMinus1.SubscriptionID, tt.nMinus1Result.SubscriptionID)
			}

			// Reverse: marshal N-1 and deserialize into current (N)
			// (simulates new code reading data written by old code)
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AzurePlatformSpec
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Location != tt.nMinus1Result.Location {
				t.Errorf("Location mismatch after N-1 round-trip: got %s, want %s", roundTripped.Location, tt.nMinus1Result.Location)
			}
			if roundTripped.OutboundType != "" {
				t.Errorf("OutboundType should be empty after N-1 round-trip: got %q", roundTripped.OutboundType)
			}
		})
	}
}
