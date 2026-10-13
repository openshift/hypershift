package aws

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
			name: "minimal configuration",
			args: []string{
				"--instance-type=m5.large",
				"--subnet-id=subnet-test123",
			},
		},
		{
			name: "full configuration",
			args: []string{
				"--instance-type=m5.xlarge",
				"--subnet-id=subnet-test456",
				"--securitygroup-id=sg-test789",
				"--instance-profile=test-worker-profile",
				"--root-volume-type=gp3",
				"--root-volume-size=150",
				"--root-volume-iops=5000",
				"--root-volume-kms-key=arn:aws:kms:us-east-1:123456789012:key/test-key",
			},
		},
		{
			name: "custom root volume configuration",
			args: []string{
				"--instance-type=m6g.large",
				"--subnet-id=subnet-arm64",
				"--root-volume-type=io2",
				"--root-volume-size=200",
				"--root-volume-iops=10000",
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
			awsOpts := DefaultOptions()

			// Bind flags
			BindDeveloperOptions(awsOpts, flags)

			// Parse flags
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			// Validate
			validOpts, err := awsOpts.Validate(ctx, coreOpts)
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
						Type: hyperv1.AWSPlatform,
					},
				},
			}

			// Create fake HostedCluster
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
								Subnet: &hyperv1.AWSResourceReference{
									ID: strPtr("subnet-default"),
								},
							},
						},
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			// Compare with fixture
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.AWS)
		})
	}
}

// TestValidate_When_root_volume_size_is_too_small_it_should_return_error tests validation logic.
func TestValidate_When_root_volume_size_is_too_small_it_should_return_error(t *testing.T) {
	opts := DefaultOptions()
	opts.RootVolumeSize = 7 // Less than minimum of 8

	_, err := opts.Validate(t.Context(), nil)
	if err == nil {
		t.Fatal("expected validation to fail for root volume size < 8")
	}

	expectedError := "root volume size must be at least 8 GB, got 7"
	if err.Error() != expectedError {
		t.Fatalf("expected error %q, got %q", expectedError, err.Error())
	}
}

// TestValidate_When_root_volume_size_is_valid_it_should_succeed tests validation success.
func TestValidate_When_root_volume_size_is_valid_it_should_succeed(t *testing.T) {
	testCases := []struct {
		name string
		size int64
	}{
		{"minimum size", 8},
		{"default size", 120},
		{"large size", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.RootVolumeSize = tc.size

			_, err := opts.Validate(t.Context(), nil)
			if err != nil {
				t.Fatalf("expected validation to succeed, got error: %v", err)
			}
		})
	}
}

// TestUpdateNodePool_When_instance_profile_is_empty_it_should_default_to_infraID tests defaulting logic.
func TestUpdateNodePool_When_instance_profile_is_empty_it_should_default_to_infraID(t *testing.T) {
	ctx := t.Context()
	opts := &CompletedAWSPlatformCreateOptions{
		completedAWSPlatformCreateOptions: &completedAWSPlatformCreateOptions{
			AWSPlatformCreateOptions: &AWSPlatformCreateOptions{
				InstanceProfile: "", // Empty
				SubnetID:        "subnet-test",
				InstanceType:    "m5.large",
				RootVolumeType:  "gp3",
				RootVolumeSize:  120,
			},
		},
	}

	nodePool := &hyperv1.NodePool{
		Spec: hyperv1.NodePoolSpec{
			Arch: "amd64",
		},
	}

	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "test-infra-id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	if err := opts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
		t.Fatalf("failed to update nodepool: %v", err)
	}

	if nodePool.Spec.Platform.AWS.InstanceProfile != "test-infra-id-worker" {
		t.Errorf("expected instance profile to be 'test-infra-id-worker', got %q", nodePool.Spec.Platform.AWS.InstanceProfile)
	}
}

// TestUpdateNodePool_When_subnet_is_empty_it_should_use_hostedcluster_subnet tests subnet defaulting.
func TestUpdateNodePool_When_subnet_is_empty_it_should_use_hostedcluster_subnet(t *testing.T) {
	ctx := t.Context()
	opts := &CompletedAWSPlatformCreateOptions{
		completedAWSPlatformCreateOptions: &completedAWSPlatformCreateOptions{
			AWSPlatformCreateOptions: &AWSPlatformCreateOptions{
				SubnetID:       "", // Empty
				InstanceType:   "m5.large",
				RootVolumeType: "gp3",
				RootVolumeSize: 120,
			},
		},
	}

	nodePool := &hyperv1.NodePool{
		Spec: hyperv1.NodePoolSpec{
			Arch: "amd64",
		},
	}

	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "test-infra",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						Subnet: &hyperv1.AWSResourceReference{
							ID: strPtr("subnet-from-hostedcluster"),
						},
					},
				},
			},
		},
	}

	if err := opts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
		t.Fatalf("failed to update nodepool: %v", err)
	}

	if *nodePool.Spec.Platform.AWS.Subnet.ID != "subnet-from-hostedcluster" {
		t.Errorf("expected subnet ID to be 'subnet-from-hostedcluster', got %q", *nodePool.Spec.Platform.AWS.Subnet.ID)
	}
}

// TestUpdateNodePool_When_instance_type_is_empty_it_should_default_based_on_arch tests instance type defaulting.
func TestUpdateNodePool_When_instance_type_is_empty_it_should_default_based_on_arch(t *testing.T) {
	testCases := []struct {
		arch                 string
		expectedInstanceType string
	}{
		{"amd64", "m5.large"},
		{"arm64", "m6g.large"},
	}

	for _, tc := range testCases {
		t.Run(tc.arch, func(t *testing.T) {
			ctx := t.Context()
			opts := &CompletedAWSPlatformCreateOptions{
				completedAWSPlatformCreateOptions: &completedAWSPlatformCreateOptions{
					AWSPlatformCreateOptions: &AWSPlatformCreateOptions{
						InstanceType:   "", // Empty
						SubnetID:       "subnet-test",
						RootVolumeType: "gp3",
						RootVolumeSize: 120,
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
						Type: hyperv1.AWSPlatform,
					},
				},
			}

			if err := opts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			if nodePool.Spec.Platform.AWS.InstanceType != tc.expectedInstanceType {
				t.Errorf("expected instance type to be %q, got %q", tc.expectedInstanceType, nodePool.Spec.Platform.AWS.InstanceType)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
