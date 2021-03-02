package create

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/infra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Commands for destroying HyperShift resources",
	}

	cmd.AddCommand(infra.NewDestroyCommand())
	cmd.AddCommand(infra.NewDestroyIAMCommand())

	return cmd
}
