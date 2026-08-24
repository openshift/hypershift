package netutil

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestConnectsThroughInternetToControlplane(t *testing.T) {
	testCases := []struct {
		name     string
		platform hyperv1.PlatformSpec
		expected bool
	}{
		{
			name:     "When platform is not AWS, it should use internet",
			expected: true,
		},
		{
			name: "When AWS endpoint access is public, it should use internet",
			platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public},
			},
			expected: true,
		},
		{
			name: "When AWS endpoint access is public and private it should not use internet",
			platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.PublicAndPrivate},
			},
		},
		{
			name: "When AWS endpoint access is private it should not use internet",
			platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private},
			},
		},
		{
			name: "When Azure topology is Public, it should use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPublic},
			},
			expected: true,
		},
		{
			name: "When Azure topology is PublicAndPrivate it should not use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPublicAndPrivate},
			},
		},
		{
			name: "When Azure topology is Private it should not use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPrivate},
			},
		},
		{
			name: "When Azure topology is empty, it should use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{},
			},
			expected: true,
		},
		{
			name:     "When Azure spec is nil, it should use internet",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ConnectsThroughInternetToControlplane(tc.platform)
			if actual != tc.expected {
				t.Errorf("expected %t, got %t", tc.expected, actual)
			}
		})
	}
}
