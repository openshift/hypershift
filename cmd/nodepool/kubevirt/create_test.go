package kubevirt

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/utils/ptr"
)

func TestKubevirtPlatformCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         KubevirtPlatformCreateOptions
		expectedError string
	}{
		{
			name: "should fail excluding default network without additional ones",
			input: KubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
			},
			expectedError: "",
		},
	} {
		var errString string
		if err := test.input.Validate(); err != nil {
			errString = err.Error()
		}
		if diff := cmp.Diff(test.expectedError, errString); diff != "" {
			t.Errorf("got incorrect error: %v", diff)
		}
	}
}

func TestKubevirtPlatformCreateOptions_Complete(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         KubevirtPlatformCreateOptions
		output        *KubevirtPlatformCompletedOptions
		expectedError string
	}{
		{
			name: "should succeed configuring additional networks",
			input: KubevirtPlatformCreateOptions{
				AdditionalNetworks: []string{
					"name:ns1/nad1",
					"name:ns2/nad2",
				},
			},
			output: &KubevirtPlatformCompletedOptions{
				AdditionalNetworks: []hypershiftv1beta1.KubevirtNetwork{
					{
						Name: "ns1/nad1",
					},
					{
						Name: "ns2/nad2",
					},
				},
			},
		},
		{
			name: "should fail with unexpected additional network parameters",
			input: KubevirtPlatformCreateOptions{
				AdditionalNetworks: []string{
					"badfield:ns2/nad2",
				},
			},
			expectedError: `failed to parse "--additional-network" flag: unknown param(s): badfield:ns2/nad2`,
		},
	} {
		var errString string
		outupt, err := test.input.Complete()
		if err != nil {
			errString = err.Error()
		}
		if diff := cmp.Diff(test.expectedError, errString); diff != "" {
			t.Errorf("got incorrect error: %v", diff)
		}
		if diff := cmp.Diff(test.output, outupt); diff != "" {
			t.Errorf("got incorrect output: %v", diff)
		}
	}
}
