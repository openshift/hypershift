package create

import (
	"github.com/spf13/cobra"

	"github.com/alknopfler/hypershift/cmd/cluster"
	"github.com/alknopfler/hypershift/cmd/infra"
	"github.com/alknopfler/hypershift/cmd/kubeconfig"
	"github.com/alknopfler/hypershift/cmd/nodepool"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HyperShift resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(cluster.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateIAMCommand())
	cmd.AddCommand(kubeconfig.NewCreateCommand())
	cmd.AddCommand(nodepool.NewCreateCommand())

	return cmd
}
