package openstack

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRawOpenStackPlatformCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawOpenStackPlatformCreateOptions
		expectedError string
	}{
		{
			name: "should fail if flavor is missing",
			input: RawOpenStackPlatformCreateOptions{
				OpenStackPlatformOptions: &OpenStackPlatformOptions{
					ImageName: "rhcos",
				},
			},
			expectedError: "flavor is required",
		},
		{
			name: "should fail if image name is missing",
			input: RawOpenStackPlatformCreateOptions{
				OpenStackPlatformOptions: &OpenStackPlatformOptions{
					Flavor: "flavor",
				},
			},
			expectedError: "image name is required",
		},
	} {
		var errString string
		if _, err := test.input.Validate(); err != nil {
			errString = err.Error()
		}
		if diff := cmp.Diff(test.expectedError, errString); diff != "" {
			t.Errorf("got incorrect error: %v", diff)
		}
	}
}
