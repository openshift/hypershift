package azureutil

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetSubnetNameFromSubnetID(t *testing.T) {
	tests := []struct {
		testCaseName       string
		subnetID           string
		expectedSubnetName string
		expectedErr        bool
	}{
		{
			testCaseName:       "empty subnet ID",
			subnetID:           "",
			expectedSubnetName: "",
			expectedErr:        true,
		},
		{
			testCaseName:       "improperly formed subnet ID",
			subnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets",
			expectedSubnetName: "",
			expectedErr:        true,
		},
		{
			testCaseName:       "properly formed subnet ID",
			subnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
			expectedSubnetName: "mySubnetName",
			expectedErr:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			subnetID, err := GetSubnetNameFromSubnetID(tc.subnetID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid subnet ID format: "+tc.subnetID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(subnetID).To(Equal(tc.expectedSubnetName))
			}
		})
	}
}

func TestGetNetworkSecurityGroupNameFromNetworkSecurityGroupID(t *testing.T) {
	tests := []struct {
		testCaseName    string
		nsgID           string
		expectedNSGName string
		expectedErr     bool
	}{
		{
			testCaseName:    "empty NSG ID",
			nsgID:           "",
			expectedNSGName: "",
			expectedErr:     true,
		},
		{
			testCaseName:    "improperly formed nsg ID",
			nsgID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups",
			expectedNSGName: "",
			expectedErr:     true,
		},
		{
			testCaseName:    "properly formed nsg ID",
			nsgID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
			expectedNSGName: "myNSGName",
			expectedErr:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			subnetID, err := GetNetworkSecurityGroupNameFromNetworkSecurityGroupID(tc.nsgID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid nsg ID format: "+tc.nsgID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(subnetID).To(Equal(tc.expectedNSGName))
			}
		})
	}
}
