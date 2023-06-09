package destroy

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/product-cli/cmd/cluster"
)

func NewCommand() *cobra.Command {
	destroyCmd := &cobra.Command{
		Use:          "destroy",
		Short:        "Commands for destroying HostedClusters",
		SilenceUsage: true,
	}

	destroyCmd.AddCommand(cluster.NewDestroyCommands())

	return destroyCmd
}
