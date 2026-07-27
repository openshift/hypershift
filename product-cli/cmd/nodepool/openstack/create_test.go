package openstack

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	openstacknodepool "github.com/openshift/hypershift/cmd/nodepool/openstack"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T)
	}{
		"When OpenStack nodepool create command is created, it should have 'openstack' as use": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Use).To(Equal("openstack"))
			},
		},
		"When OpenStack nodepool create command is created, it should register nodepool flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("openstack-node-flavor")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-node-image-name")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-node-availability-zone")).ToNot(BeNil())
			},
		},
		"When OpenStack nodepool create command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				expectedFlags := []string{
					"openstack-node-additional-port",
					"openstack-node-availability-zone",
					"openstack-node-flavor",
					"openstack-node-image-name",
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
		name        string
		args        []string
		expectError bool
	}{
		{
			name: "When minimal configuration is provided, it should generate correct nodepool",
			args: []string{
				"--openstack-node-flavor=m1.large",
				"--openstack-node-image-name=rhcos-openstack",
			},
		},
		{
			name: "When full configuration with availability zone is provided, it should generate correct nodepool",
			args: []string{
				"--openstack-node-flavor=m1.xlarge",
				"--openstack-node-image-name=rhcos-openstack-latest",
				"--openstack-node-availability-zone=nova",
				"--openstack-node-additional-port=network-id:40a355cb-596d-495c-8766-419d98cadd57",
			},
		},
		{
			name:        "When flavor is empty, it should return a validation error",
			args:        []string{},
			expectError: true,
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
			openstackOpts := openstacknodepool.DefaultOptions()
			openstacknodepool.BindOptions(openstackOpts, flags)

			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			validOpts, err := openstackOpts.Validate(ctx, coreOpts)
			if testCase.expectError {
				g := NewWithT(t)
				g.Expect(err).To(HaveOccurred())
				return
			}
			if err != nil {
				t.Fatalf("validation failed: %v", err)
			}

			completedOpts, err := validOpts.Complete(ctx, coreOpts)
			if err != nil {
				t.Fatalf("completion failed: %v", err)
			}

			// Platform.Type is intentionally omitted here because OpenStack's
			// UpdateNodePool sets it internally, unlike other platforms.
			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: coreOpts.Arch,
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			g := NewWithT(t)
			g.Expect(nodePool.Spec.Platform.Type).To(Equal(hyperv1.OpenStackPlatform))
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.OpenStack)
		})
	}
}
