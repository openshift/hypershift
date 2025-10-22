package create

import (
	"github.com/openshift/hypershift/cmd/backup"
	"github.com/openshift/hypershift/cmd/restore"
	"github.com/openshift/hypershift/product-cli/cmd/cluster"
	"github.com/openshift/hypershift/product-cli/cmd/kubeconfig"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(backup.NewCreateCommand())
	cmd.AddCommand(cluster.NewCreateCommands())
	cmd.AddCommand(kubeconfig.NewCreateCommand())
	cmd.AddCommand(nodepool.NewCreateCommand())
	cmd.AddCommand(restore.NewCreateCommand())

	return cmd
}
