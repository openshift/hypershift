package kubevirt

import (
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"

	"github.com/google/go-cmp/cmp"
)

func TestRawKubevirtPlatformCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawKubevirtPlatformCreateOptions
		expectedError string
	}{
		{
			name: "should fail excluding default network without additional ones",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
			},
			expectedError: "",
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

func TestValidatedKubevirtPlatformCreateOptions_Complete(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawKubevirtPlatformCreateOptions
		output        []hypershiftv1beta1.KubevirtNetwork
		expectedError string
	}{
		{
			name: "should succeed configuring additional networks",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
				AdditionalNetworks: []string{
					"name:ns1/nad1",
					"name:ns2/nad2",
				},
			},
			output: []hypershiftv1beta1.KubevirtNetwork{
				{
					Name: "ns1/nad1",
				},
				{
					Name: "ns2/nad2",
				},
			},
		},
		{
			name: "should fail with unexpected additional network parameters",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
				AdditionalNetworks: []string{
					"badfield:ns2/nad2",
				},
			},
			expectedError: `failed to parse "--additional-network" flag: unknown param(s): badfield:ns2/nad2`,
		},
		{
			name: "should succeed configuring NetworkInterfaceMultiQueue=Enable",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
				AdditionalNetworks: []string{
					"name:ns1/nad1",
					"name:ns2/nad2",
				},
				NetworkInterfaceMultiQueue: "Enable",
			},
			output: []hypershiftv1beta1.KubevirtNetwork{
				{
					Name: "ns1/nad1",
				},
				{
					Name: "ns2/nad2",
				},
			},
		},
		{
			name: "should succeed configuring NetworkInterfaceMultiQueue=Disable",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
				AdditionalNetworks: []string{
					"name:ns1/nad1",
					"name:ns2/nad2",
				},
				NetworkInterfaceMultiQueue: "Disable",
			},
			output: []hypershiftv1beta1.KubevirtNetwork{
				{
					Name: "ns1/nad1",
				},
				{
					Name: "ns2/nad2",
				},
			},
		},
		{
			name: "should fail configuring NetworkInterfaceMultiQueue",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:                1,
					RootVolumeSize:       16,
					AttachDefaultNetwork: ptr.To(true),
				},
				AdditionalNetworks: []string{
					"name:ns1/nad1",
					"name:ns2/nad2",
				},
				NetworkInterfaceMultiQueue: "wrong",
			},
			expectedError: `wrong value for the --network-multiqueue parameter. Supported values are "Enable" or "Disable"`,
		},
		{
			name: "should succeed configuring two Host Devices",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:          2,
					RootVolumeSize: 32,
				},
				HostDevices: []string{
					"my-great-gpu,count:2",
					"my-soundcard",
				},
			},
		},
		{
			name: "should fail configuring Host Devices without misspelled count",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:          2,
					RootVolumeSize: 32,
				},
				HostDevices: []string{
					"my-fabulous-gpu,cuont:2",
				},
			},
			expectedError: "invalid KubeVirt host device setting: [my-fabulous-gpu,cuont:2]",
		},
		{
			name: "should fail configuring Host Devices with an unsupported option",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:          2,
					RootVolumeSize: 32,
				},
				HostDevices: []string{
					"my-fabulous-gpu,count:2,speed:100GFLOPS",
				},
			},
			expectedError: "invalid KubeVirt host device setting: [my-fabulous-gpu,count:2,speed:100GFLOPS]",
		},
		{
			name: "should fail configuring Host Devices with a non-integer count",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:          2,
					RootVolumeSize: 32,
				},
				HostDevices: []string{
					"my-fabulous-gpu,count:1K",
				},
			},
			expectedError: "could not parse host device count: [my-fabulous-gpu,count:1K]",
		},
		{
			name: "should fail configuring Host Devices with a negative count",
			input: RawKubevirtPlatformCreateOptions{
				KubevirtPlatformOptions: &KubevirtPlatformOptions{
					Cores:          2,
					RootVolumeSize: 32,
				},
				HostDevices: []string{
					"my-fabulous-gpu,count:-8",
				},
			},
			expectedError: "host device count must be greater than or equal to 1. received: [-8]",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			validated, err := test.input.Validate()
			if err != nil {
				t.Fatalf("Validate() failed: %v", err)
			}
			var errString string
			output, err := validated.Complete()
			if err != nil {
				errString = err.Error()
			}
			if diff := cmp.Diff(test.expectedError, errString); diff != "" {
				t.Errorf("got incorrect error: %v", diff)
			}
			var got []hypershiftv1beta1.KubevirtNetwork
			if output != nil && output.completetedKubevirtPlatformCreateOptions != nil {
				got = output.completetedKubevirtPlatformCreateOptions.AdditionalNetworks
			}
			if diff := cmp.Diff(test.output, got); diff != "" {
				t.Errorf("got incorrect output: %v", diff)
			}
		})
	}
}
