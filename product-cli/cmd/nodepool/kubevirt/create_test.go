package kubevirt

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	kubevirtnodepool "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T)
	}{
		"When KubeVirt nodepool create command is created, it should have 'kubevirt' as use": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Use).To(Equal("kubevirt"))
			},
		},
		"When KubeVirt nodepool create command is created, it should default memory to 4Gi": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("memory").DefValue).To(Equal("4Gi"))
			},
		},
		"When KubeVirt nodepool create command is created, it should default cores to 2": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("cores").DefValue).To(Equal("2"))
			},
		},
		"When KubeVirt nodepool create command is created, it should default root-volume-size to 32": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("root-volume-size").DefValue).To(Equal("32"))
			},
		},
		"When KubeVirt nodepool create command is created, it should default qos-class to Burstable": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("qos-class").DefValue).To(Equal("Burstable"))
			},
		},
		"When KubeVirt nodepool create command is created, it should default network-multiqueue to Enable": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("network-multiqueue").DefValue).To(Equal("Enable"))
			},
		},
		"When KubeVirt nodepool create command is created, it should default attach-default-network to true": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("attach-default-network").DefValue).To(Equal("true"))
			},
		},
		"When KubeVirt nodepool create command is created, it should not register developer-only flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("containerdisk")).To(BeNil(), "containerdisk is a developer-only flag")
			},
		},
		"When KubeVirt nodepool create command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				expectedFlags := []string{
					"additional-network",
					"attach-default-network",
					"cores",
					"host-device-name",
					"memory",
					"network-multiqueue",
					"qos-class",
					"root-volume-access-modes",
					"root-volume-cache-strategy",
					"root-volume-size",
					"root-volume-storage-class",
					"root-volume-volume-mode",
					"vm-node-selector",
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
				"--cores=2",
				"--memory=4Gi",
				"--root-volume-size=32",
			},
		},
		{
			name: "When full configuration is provided, it should generate correct nodepool",
			args: []string{
				"--cores=4",
				"--memory=8Gi",
				"--root-volume-size=64",
				"--root-volume-storage-class=fast-storage",
				"--root-volume-access-modes=ReadWriteOnce",
				"--root-volume-volume-mode=Block",
				"--root-volume-cache-strategy=None",
				"--network-multiqueue=Enable",
				"--qos-class=Guaranteed",
				"--additional-network=name:default/nad1",
				"--attach-default-network=false",
				"--vm-node-selector=role=kubevirt,size=large",
				"--host-device-name=testdevice,count:1",
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
			kubevirtOpts := kubevirtnodepool.DefaultOptions()
			kubevirtnodepool.BindOptions(kubevirtOpts, flags)

			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			validOpts, err := kubevirtOpts.Validate(ctx, coreOpts)
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
						Type: hyperv1.KubevirtPlatform,
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			testutil.CompareWithFixture(t, nodePool.Spec.Platform.Kubevirt)
		})
	}
}
