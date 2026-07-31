package agent

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When Agent create command is created, it should have 'agent' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Use).To(Equal("agent"))
			},
		},
		{
			name: "When Agent create command is created, it should mark agent-namespace as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				agentNSFlag := cmd.Flag("agent-namespace")
				g.Expect(agentNSFlag).ToNot(BeNil())
				g.Expect(agentNSFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(agentNSFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When Agent create command is added to a parent, pull-secret should be inherited",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()

				parent := &cobra.Command{Use: "cluster"}
				core.BindOptions(opts, parent.PersistentFlags())

				cmd := NewCreateCommand(opts)
				parent.AddCommand(cmd)

				pullSecretFlag := cmd.InheritedFlags().Lookup("pull-secret")
				g.Expect(pullSecretFlag).ToNot(BeNil(), "pull-secret should be inherited from parent command")
			},
		},
		{
			name: "When Agent create command is created, it should register exactly the expected flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				expectedFlags := []string{
					"agent-namespace",
					"agentLabelSelector",
					"api-server-address",
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.test(t)
		})
	}
}

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(t.Context())

	tempDir := t.TempDir()

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")
	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal flags are provided, it should render correctly",
			args: []string{
				"--api-server-address=fakeAddress",
				"--render-sensitive",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindOptions(coreOpts, flags)
			agentOpts := agent.DefaultOptions()
			agent.BindOptions(agentOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, agentOpts); err != nil {
				t.Fatalf("failed to create cluster: %v", err)
			}

			manifests, err := os.ReadFile(manifestsFile)
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			testutil.CompareWithFixture(t, manifests)
		})
	}
}
