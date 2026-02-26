package util

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
			name:     "Not aws always uses internet",
			expected: true,
		},
		{
			name: "AWS public uses internet",
			platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public},
			},
			expected: true,
		},
		{
			name: "AWS public/private doesn't use internet",
			platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.PublicAndPrivate},
			},
		},
		{
			name: "AWS private doesn't use internet",
			platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private},
			},
		},
		{
			name: "When Azure endpoint access is Public it should use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{EndpointAccess: hyperv1.AzureEndpointAccessPublic},
			},
			expected: true,
		},
		{
			name: "When Azure endpoint access is PublicAndPrivate it should not use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{EndpointAccess: hyperv1.AzureEndpointAccessPublicAndPrivate},
			},
		},
		{
			name: "When Azure endpoint access is Private it should not use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{EndpointAccess: hyperv1.AzureEndpointAccessPrivate},
			},
		},
		{
			name: "When Azure endpoint access is empty it should use internet",
			platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{},
			},
			expected: true,
		},
		{
			name:     "When Azure spec is nil it should use internet",
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
