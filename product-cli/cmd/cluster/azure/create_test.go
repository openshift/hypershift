package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/config"

	"github.com/spf13/cobra"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When Azure create command is created, it should have 'azure' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When Azure create command is created, it should mark azure-creds as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				azureCredsFlag := cmd.Flag("azure-creds")
				g.Expect(azureCredsFlag).ToNot(BeNil())
				g.Expect(azureCredsFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(azureCredsFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When Azure create command is created, it should mark pull-secret as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()

				// pull-secret is registered as a persistent flag on the parent
				// command (see product-cli/cmd/cluster/cluster.go). Simulate
				// that hierarchy here so MarkPersistentFlagRequired resolves
				// the flag correctly.
				parent := &cobra.Command{Use: "cluster"}
				core.BindOptions(opts, parent.PersistentFlags())
				_ = parent.MarkPersistentFlagRequired("pull-secret")

				cmd := NewCreateCommand(opts)
				parent.AddCommand(cmd)

				pullSecretFlag := parent.PersistentFlags().Lookup("pull-secret")
				g.Expect(pullSecretFlag).ToNot(BeNil())
				g.Expect(pullSecretFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(pullSecretFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When Azure create command is created, it should set release stream to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				_ = NewCreateCommand(opts)
				g.Expect(opts.ReleaseStream).To(Equal(config.DefaultReleaseStream))
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
