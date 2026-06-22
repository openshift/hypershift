package openstack

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
)

// TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool tests the full CLI flag parsing → Validate() → Complete() → NodePool manifest generation flow.
func TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal configuration",
			args: []string{
				"--openstack-node-flavor=m1.large",
				"--openstack-node-image-name=rhcos-openstack",
			},
		},
		{
			name: "full configuration with availability zone",
			args: []string{
				"--openstack-node-flavor=m1.xlarge",
				"--openstack-node-image-name=rhcos-openstack-latest",
				"--openstack-node-availability-zone=nova",
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
			openstackOpts := DefaultOptions()

			// Bind flags
			BindOptions(openstackOpts, flags)

			// Parse flags
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			// Validate
			validOpts, err := openstackOpts.Validate(ctx, coreOpts)
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
						Type: hyperv1.OpenStackPlatform,
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			// Compare with fixture
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.OpenStack)
		})
	}
}

func TestRawOpenStackPlatformCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawOpenStackPlatformCreateOptions
		expectedError string
	}{
		{
			name: "should fail if flavor is missing",
			input: RawOpenStackPlatformCreateOptions{
				OpenStackPlatformOptions: &OpenStackPlatformOptions{},
			},
			expectedError: "flavor is required",
		},
		{
			name: "should pass when AZ is provided",
			input: RawOpenStackPlatformCreateOptions{
				OpenStackPlatformOptions: &OpenStackPlatformOptions{
					Flavor:         "flavor",
					ImageName:      "rhcos",
					AvailabityZone: "az",
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
