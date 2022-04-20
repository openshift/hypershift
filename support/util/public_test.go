package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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
