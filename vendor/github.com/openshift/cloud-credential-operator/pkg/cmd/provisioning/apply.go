package provisioning

import (
	"github.com/spf13/cobra"
)

// NewApplyCmd provides the "apply" subcommand, grouping commands that push previously
// generated manifests to a cluster.
func NewApplyCmd() *cobra.Command {
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply previously generated manifests to a cluster",
	}

	applyCmd.AddCommand(NewApplySecretsCmd())

	return applyCmd
}
