package infra

import (
	"github.com/spf13/cobra"

	"github.com/alknopfler/hypershift/cmd/infra/aws"
)

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "infra",
		Short:        "Commands for destroying HyperShift infra resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewDestroyCommand())

	return cmd
}
