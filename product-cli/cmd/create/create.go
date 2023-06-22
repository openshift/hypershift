package create

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/product-cli/cmd/cluster"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(cluster.NewCreateCommands())
	cmd.AddCommand(nodepool.NewCreateCommand())

	return cmd
}
