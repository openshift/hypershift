package gcp

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/spf13/pflag"
)

// TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool tests the full CLI flag parsing → Validate() → Complete() → NodePool manifest generation flow.
func TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal flags are provided, it should use defaults",
			args: []string{
				"--machine-type=n2-standard-4",
				"--zone=us-central1-a",
				"--subnet=test-subnet",
			},
		},
		{
			name: "When all flags are provided, it should set all fields",
			args: []string{
				"--machine-type=n2-standard-8",
				"--zone=us-west1-b",
				"--subnet=prod-subnet",
				"--boot-disk-size=200",
				"--boot-disk-type=pd-ssd",
				"--boot-disk-encryption-key=projects/test-project/locations/us-west1/keyRings/test-ring/cryptoKeys/test-key",
				"--service-account-email=test-sa@test-project.iam.gserviceaccount.com",
				"--provisioning-model=Spot",
				"--network-tags=tag1,tag2",
				"--resource-labels=env=prod,team=platform",
				"--image=projects/rhcos-cloud/global/images/rhcos-test",
			},
		},
		{
			name: "When custom boot disk flags are provided, it should customize boot disk",
			args: []string{
				"--machine-type=n2-standard-16",
				"--zone=europe-west1-c",
				"--subnet=custom-subnet",
				"--boot-disk-size=500",
				"--boot-disk-type=pd-balanced",
				"--provisioning-model=Preemptible",
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
				Arch:        "amd64",
			}
			gcpOpts := DefaultOptions()

			// Bind flags
			BindDeveloperOptions(gcpOpts, flags)

			// Parse flags
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			// Validate
			validOpts, err := gcpOpts.Validate(ctx, coreOpts)
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
						Type: hyperv1.GCPPlatform,
					},
				},
			}

			// Create fake HostedCluster
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP:  &hyperv1.GCPPlatformSpec{},
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			// Compare with fixture
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.GCP)
		})
	}
}

// TestValidate_When_boot_disk_size_is_too_small_it_should_return_error tests validation logic.
func TestValidate_When_boot_disk_size_is_too_small_it_should_return_error(t *testing.T) {
	opts := DefaultOptions()
	opts.BootDiskSize = 7 // Less than minimum of 8

	_, err := opts.Validate(t.Context(), nil)
	if err == nil {
		t.Fatal("expected validation to fail for boot disk size < 8")
	}

	expectedError := "boot disk size must be at least 8 GB, got 7"
	if err.Error() != expectedError {
		t.Fatalf("expected error %q, got %q", expectedError, err.Error())
	}
}

// TestValidate_When_boot_disk_size_is_valid_it_should_succeed tests validation success.
func TestValidate_When_boot_disk_size_is_valid_it_should_succeed(t *testing.T) {
	testCases := []struct {
		name string
		size int32
	}{
		{"minimum size", 8},
		{"default size", 120},
		{"large size", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.BootDiskSize = tc.size

			_, err := opts.Validate(t.Context(), nil)
			if err != nil {
				t.Fatalf("expected validation to succeed, got error: %v", err)
			}
		})
	}
}

// TestValidate_When_zone_format_is_invalid_it_should_return_error tests zone validation.
func TestValidate_When_zone_format_is_invalid_it_should_return_error(t *testing.T) {
	testCases := []struct {
		name          string
		zone          string
		expectedError string
	}{
		{
			name:          "missing zone suffix",
			zone:          "us-central1",
			expectedError: "zone must be in the form of region-zone (e.g., us-central1-a), got \"us-central1\"",
		},
		{
			name:          "invalid characters",
			zone:          "us_central1-a",
			expectedError: "zone must be in the form of region-zone (e.g., us-central1-a), got \"us_central1-a\"",
		},
		{
			name:          "uppercase zone",
			zone:          "US-CENTRAL1-A",
			expectedError: "zone must be in the form of region-zone (e.g., us-central1-a), got \"US-CENTRAL1-A\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Zone = tc.zone

			_, err := opts.Validate(t.Context(), nil)
			if err == nil {
				t.Fatal("expected validation to fail for invalid zone format")
			}

			if err.Error() != tc.expectedError {
				t.Fatalf("expected error %q, got %q", tc.expectedError, err.Error())
			}
		})
	}
}

// TestValidate_When_machine_type_format_is_invalid_it_should_return_error tests machine type validation.
func TestValidate_When_machine_type_format_is_invalid_it_should_return_error(t *testing.T) {
	testCases := []struct {
		name          string
		machineType   string
		expectedError string
	}{
		{
			name:          "uppercase characters",
			machineType:   "N2-STANDARD-4",
			expectedError: "machine type must contain only lowercase letters, digits, and hyphens, got \"N2-STANDARD-4\"",
		},
		{
			name:          "invalid characters",
			machineType:   "n2_standard_4",
			expectedError: "machine type must contain only lowercase letters, digits, and hyphens, got \"n2_standard_4\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.MachineType = tc.machineType

			_, err := opts.Validate(t.Context(), nil)
			if err == nil {
				t.Fatal("expected validation to fail for invalid machine type format")
			}

			if err.Error() != tc.expectedError {
				t.Fatalf("expected error %q, got %q", tc.expectedError, err.Error())
			}
		})
	}
}

// TestValidate_When_provisioning_model_is_invalid_it_should_return_error tests provisioning model validation.
func TestValidate_When_provisioning_model_is_invalid_it_should_return_error(t *testing.T) {
	opts := DefaultOptions()
	opts.ProvisioningModel = "InvalidModel"

	_, err := opts.Validate(t.Context(), nil)
	if err == nil {
		t.Fatal("expected validation to fail for invalid provisioning model")
	}

	expectedError := "provisioning model must be one of [Standard Spot Preemptible], got \"InvalidModel\""
	if err.Error() != expectedError {
		t.Fatalf("expected error %q, got %q", expectedError, err.Error())
	}
}

// TestUpdateNodePool_When_machine_type_is_empty_it_should_default_based_on_arch tests machine type defaulting.
func TestUpdateNodePool_When_machine_type_is_empty_it_should_default_based_on_arch(t *testing.T) {
	testCases := []struct {
		arch                string
		expectedMachineType string
	}{
		{"amd64", "n2-standard-4"},
		{"arm64", "t2a-standard-4"}, // Tau T2A family for ARM64
	}

	for _, tc := range testCases {
		t.Run(tc.arch, func(t *testing.T) {
			ctx := t.Context()
			opts := &CompletedGCPNodePoolCreateOptions{
				completedGCPNodePoolCreateOptions: &completedGCPNodePoolCreateOptions{
					GCPNodePoolCreateOptions: &GCPNodePoolCreateOptions{
						MachineType:       "", // Empty
						Zone:              "us-central1-a",
						Subnet:            "test-subnet",
						BootDiskSize:      120,
						BootDiskType:      "pd-standard",
						ProvisioningModel: "Standard",
						ResourceLabels:    make(map[string]string),
						NetworkTags:       []string{},
					},
				},
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: tc.arch,
				},
			}

			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
					},
				},
			}

			if err := opts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			if nodePool.Spec.Platform.GCP.MachineType != tc.expectedMachineType {
				t.Errorf("expected machine type to be %q, got %q", tc.expectedMachineType, nodePool.Spec.Platform.GCP.MachineType)
			}
		})
	}
}
