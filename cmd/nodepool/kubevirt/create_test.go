package kubevirt

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"k8s.io/utils/ptr"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
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
		if _, err := test.input.Validate(t.Context(), nil); err != nil {
			errString = err.Error()
		}
		if diff := cmp.Diff(test.expectedError, errString); diff != "" {
			t.Errorf("got incorrect error: %v", diff)
		}
	}
}

// TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool tests the full CLI flag parsing → Validate() → Complete() → NodePool manifest generation flow.
func TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal configuration",
			args: []string{
				"--cores=2",
				"--memory=4Gi",
				"--root-volume-size=32",
			},
		},
		{
			name: "full configuration with additional networks",
			args: []string{
				"--cores=4",
				"--memory=8Gi",
				"--root-volume-size=64",
				"--root-volume-storage-class=fast-storage",
				"--root-volume-access-modes=ReadWriteOnce",
				"--root-volume-volume-mode=Block",
				"--network-multiqueue=Enable",
				"--qos-class=Guaranteed",
				"--additional-network=name:default/nad1",
				"--additional-network=name:default/nad2",
				"--attach-default-network=false",
			},
		},
		{
			name: "with host devices",
			args: []string{
				"--cores=8",
				"--memory=16Gi",
				"--root-volume-size=128",
				"--host-device-name=gpu-device,count:2",
				"--qos-class=Burstable",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			// Setup flag parsing
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := &core.CreateNodePoolOptions{
				Name:        "test-nodepool",
				Namespace:   "clusters",
				ClusterName: "test-cluster",
				Replicas:    3,
				Arch:        string(hyperv1.ArchitectureAMD64),
			}
			kubevirtOpts := DefaultOptions()

			// Bind flags
			BindDeveloperOptions(kubevirtOpts, flags)

			// Parse flags
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			// Validate
			validOpts, err := kubevirtOpts.Validate(ctx, coreOpts)
			if err != nil {
				t.Fatalf("validation failed: %v", err)
			}

			// Complete
			completedOpts, err := validOpts.Complete(ctx, coreOpts)
			if err != nil {
				t.Fatalf("completion failed: %v", err)
			}

			// Generate NodePool
			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: coreOpts.Arch,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			// Compare with fixture
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.Kubevirt)
		})
	}
}

func TestValidatedKubevirtPlatformCreateOptions_Complete(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawKubevirtPlatformCreateOptions
		output        []hyperv1.KubevirtNetwork
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
			output: []hyperv1.KubevirtNetwork{
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
			output: []hyperv1.KubevirtNetwork{
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
			output: []hyperv1.KubevirtNetwork{
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
			validated, err := test.input.Validate(t.Context(), nil)
			if err != nil {
				t.Fatalf("Validate() failed: %v", err)
			}
			var errString string
			platformOpts, err := validated.Complete(t.Context(), nil)
			if err != nil {
				errString = err.Error()
			}
			if diff := cmp.Diff(test.expectedError, errString); diff != "" {
				t.Errorf("got incorrect error: %v", diff)
			}
			var got []hyperv1.KubevirtNetwork
			// Type assert to get the completed options back
			if output, ok := platformOpts.(*CompletedKubevirtPlatformCreateOptions); ok && output != nil && output.completedKubevirtPlatformCreateOptions != nil {
				got = output.completedKubevirtPlatformCreateOptions.AdditionalNetworks
			}
			if diff := cmp.Diff(test.output, got); diff != "" {
				t.Errorf("got incorrect output: %v", diff)
			}
		})
	}
}
