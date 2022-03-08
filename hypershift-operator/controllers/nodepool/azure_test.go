package nodepool

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestBootImage(t *testing.T) {
	testCases := []struct {
		name     string
		hcluster *hyperv1.HostedCluster
		nodepool *hyperv1.NodePool
		expected string
	}{
		{
			name: "Nodepool has image set, it is being used",
			nodepool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{Azure: &hyperv1.AzureNodePoolPlatform{
				ImageID: "nodepool-image",
			}}}},
			expected: "nodepool-image",
		},
		{
			name: "Default bootimage is used",
			hcluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Azure: &hyperv1.AzurePlatformSpec{
				SubscriptionID:    "123-123",
				ResourceGroupName: "rg-name",
			}}}},
			nodepool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{Azure: &hyperv1.AzureNodePoolPlatform{}}}},
			expected: "/subscriptions/123-123/resourceGroups/rg-name/providers/Microsoft.Compute/images/rhcos.x86_64.vhd",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := bootImage(tc.hcluster, tc.nodepool)
			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}
