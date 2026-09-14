package kubevirt

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
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
			name: "When KubeVirt create command is created, it should have 'kubevirt' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Use).To(Equal("kubevirt"))
			},
		},
		{
			name: "When KubeVirt create command is created, it should register infra-kubeconfig-file flag",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Flag("infra-kubeconfig-file")).ToNot(BeNil())
			},
		},
		{
			name: "When KubeVirt create command is created, it should register nodepool flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("memory")).ToNot(BeNil())
				g.Expect(cmd.Flag("cores")).ToNot(BeNil())
				g.Expect(cmd.Flag("root-volume-storage-class")).ToNot(BeNil())
				g.Expect(cmd.Flag("root-volume-size")).ToNot(BeNil())
			},
		},
		{
			name: "When KubeVirt create command is created, it should not register developer-only flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("api-server-address")).To(BeNil(), "api-server-address is a developer-only flag")
				g.Expect(cmd.Flag("service-publishing-strategy")).To(BeNil(), "service-publishing-strategy is a developer-only flag")
				g.Expect(cmd.Flag("containerdisk")).To(BeNil(), "containerdisk is a developer-only flag")
			},
		},
		{
			name: "When KubeVirt create command is added to a parent, pull-secret should be inherited",
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
			name: "When KubeVirt create command is created, it should register exactly the expected flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				expectedFlags := []string{
					"additional-network",
					"attach-default-network",
					"cores",
					"host-device-name",
					"infra-kubeconfig-file",
					"infra-namespace",
					"infra-storage-class-mapping",
					"infra-volumesnapshot-class-mapping",
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
	t.Setenv("FAKE_CLIENT", "true")

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
			kubevirtOpts := kubevirt.DefaultOptions()
			kubevirt.BindOptions(kubevirtOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, kubevirtOpts); err != nil {
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
