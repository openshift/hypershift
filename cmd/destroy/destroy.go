package create

import (
	"github.com/spf13/cobra"

	"github.com/alknopfler/hypershift/cmd/cluster"
	"github.com/alknopfler/hypershift/cmd/infra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "destroy",
		Short:        "Commands for destroying HyperShift resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(infra.NewDestroyCommand())
	cmd.AddCommand(infra.NewDestroyIAMCommand())
	cmd.AddCommand(cluster.NewDestroyCommand())

	return cmd
}
