package kubeconfig

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hypershiftkubeconfig "github.com/openshift/hypershift/cmd/kubeconfig"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "When kubeconfig create command is created, it should have 'kubeconfig' as use",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Use).To(Equal("kubeconfig"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should set long description from shared package",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Long).To(Equal(hypershiftkubeconfig.Description))
			},
		},
		{
			name: "When kubeconfig create command is created, it should silence usage on error",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.SilenceUsage).To(BeTrue())
			},
		},
		{
			name: "When kubeconfig create command is created, it should wire a RunE handler",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.RunE).ToNot(BeNil())
			},
		},
		{
			name: "When kubeconfig create command is created, it should default namespace to clusters",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				flag := cmd.Flag("namespace")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("clusters"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should register name flag with empty default",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				flag := cmd.Flag("name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(BeEmpty())
			},
		},
		{
			name: "When kubeconfig create command is created, it should default port-forward to false",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				flag := cmd.Flag("port-forward")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("false"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should register exactly the expected flags",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				expectedFlags := []string{
					"name",
					"namespace",
					"port-forward",
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
			cmd := NewCreateCommand()
			tt.test(t, cmd)
		})
	}
}
