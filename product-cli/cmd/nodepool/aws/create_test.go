package aws

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftaws "github.com/openshift/hypershift/cmd/nodepool/aws"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"k8s.io/utils/ptr"

	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T)
	}{
		"When AWS nodepool create command is created, it should have 'aws' as use": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Use).To(Equal("aws"))
			},
		},
		"When AWS nodepool create command is created, it should default root-volume-type to gp3": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("root-volume-type").DefValue).To(Equal("gp3"))
			},
		},
		"When AWS nodepool create command is created, it should default root-volume-size to 120": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("root-volume-size").DefValue).To(Equal("120"))
			},
		},
		"When AWS nodepool create command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				expectedFlags := []string{
					"instance-profile",
					"instance-type",
					"root-volume-iops",
					"root-volume-kms-key",
					"root-volume-size",
					"root-volume-type",
					"securitygroup-id",
					"subnet-id",
				}
				var actualFlags []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				sort.Strings(actualFlags)
				g.Expect(actualFlags).To(Equal(expectedFlags))
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			test.verify(t)
		})
	}
}

func TestUpdateNodePool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal configuration is provided, it should generate correct nodepool",
			args: []string{
				"--instance-type=m5.large",
				"--subnet-id=subnet-test123",
			},
		},
		{
			name: "When full configuration is provided, it should generate correct nodepool",
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
			name: "When subnet-id is omitted, it should use the hosted cluster default subnet",
			args: []string{
				"--instance-type=m5.large",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := &core.CreateNodePoolOptions{
				Name:        "test-nodepool",
				Namespace:   "clusters",
				ClusterName: "test-cluster",
				Replicas:    3,
				Arch:        string(hyperv1.ArchitectureAMD64),
			}
			awsOpts := hypershiftaws.DefaultOptions()
			hypershiftaws.BindDeveloperOptions(awsOpts, flags)

			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			validOpts, err := awsOpts.Validate(ctx, coreOpts)
			if err != nil {
				t.Fatalf("validation failed: %v", err)
			}

			completedOpts, err := validOpts.Complete(ctx, coreOpts)
			if err != nil {
				t.Fatalf("completion failed: %v", err)
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: coreOpts.Arch,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
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
									ID: ptr.To("subnet-default"),
								},
							},
						},
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			testutil.CompareWithFixture(t, nodePool.Spec.Platform.AWS)
		})
	}
}
