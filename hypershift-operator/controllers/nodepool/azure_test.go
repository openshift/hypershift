package nodepool

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func TestBootImage(t *testing.T) {
	testCases := []struct {
		name              string
		subscriptionID    string
		resourceGroupName string
		expected          string
	}{
		{
			name:              "Default amd64 boot image is used",
			subscriptionID:    "123-123",
			resourceGroupName: "rg-name",
			expected:          "/subscriptions/123-123/resourceGroups/rg-name/providers/Microsoft.Compute/galleries/" + hyperv1.AzureGalleryName + "_name/images/RHCOS_Amd64/versions/1.0.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := bootImage(tc.subscriptionID, tc.resourceGroupName)
			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}
