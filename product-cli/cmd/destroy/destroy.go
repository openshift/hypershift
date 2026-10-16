package destroy

import (
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/product-cli/cmd/cluster"
	"github.com/openshift/hypershift/product-cli/cmd/iam"
	"github.com/openshift/hypershift/product-cli/cmd/infra"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool"

	"github.com/spf13/cobra"
)

func NewCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	destroyCmd := &cobra.Command{
		Use:          "destroy",
		Short:        "Commands for destroying HostedClusters and NodePools",
		SilenceUsage: true,
	}

	destroyCmd.AddCommand(cluster.NewDestroyCommands(clientProvider))
	destroyCmd.AddCommand(iam.NewDestroyCommands())
	destroyCmd.AddCommand(infra.NewDestroyCommands())
	destroyCmd.AddCommand(nodepool.NewDestroyCommand(clientProvider))

	return destroyCmd
}
