package agent

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftagent "github.com/openshift/hypershift/cmd/nodepool/agent"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T)
	}{
		"When Agent nodepool create command is created, it should have 'agent' as use": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Use).To(Equal("agent"))
			},
		},
		"When Agent nodepool create command is created, it should register agentLabelSelector flag": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("agentLabelSelector")).ToNot(BeNil())
			},
		},
		"When Agent nodepool create command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				expectedFlags := []string{
					"agentLabelSelector",
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
		name               string
		agentLabelSelector string
	}{
		{
			name:               "When empty label selector is provided, it should generate correct nodepool",
			agentLabelSelector: "",
		},
		{
			name:               "When single label selector is provided, it should generate correct nodepool",
			agentLabelSelector: "size=large",
		},
		{
			name:               "When multi label selector is provided, it should generate correct nodepool",
			agentLabelSelector: "size=large,zone notin (az1,az2)",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			// Agent platform does not implement the DefaultOptions → Validate → Complete
			// pipeline used by other platforms; it only exposes NewAgentPlatformCreateOptions
			// with a single AgentLabelSelector field, so we wire the flag value manually.
			cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
			if testCase.agentLabelSelector != "" {
				if err := cmd.Flags().Parse([]string{"--agentLabelSelector=" + testCase.agentLabelSelector}); err != nil {
					t.Fatalf("failed to parse flags: %v", err)
				}
			}

			platformOpts := hypershiftagent.NewAgentPlatformCreateOptions(&cobra.Command{})
			platformOpts.AgentLabelSelector = cmd.Flags().Lookup("agentLabelSelector").Value.String()

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: string(hyperv1.ArchitectureAMD64),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
				},
			}

			if err := platformOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			testutil.CompareWithFixture(t, nodePool.Spec.Platform.Agent)
		})
	}
}
