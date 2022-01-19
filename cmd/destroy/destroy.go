package aws

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/bastion"
	"github.com/openshift/hypershift/cmd/cluster"
	"github.com/openshift/hypershift/cmd/infra"
)

func NewCommand() *cobra.Command {
	destroyCmd := &cobra.Command{
		Use:          "destroy",
		Short:        "Commands for destroying HyperShift resources",
		SilenceUsage: true,
	}

	destroyCmd.AddCommand(cluster.NewDestroyCommands())
	destroyCmd.AddCommand(infra.NewDestroyCommand())
	destroyCmd.AddCommand(infra.NewDestroyIAMCommand())
	destroyCmd.AddCommand(bastion.NewDestroyCommand())

	return destroyCmd
}
