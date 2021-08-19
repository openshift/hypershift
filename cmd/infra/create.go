package infra

import (
	"github.com/spf13/cobra"

	"github.com/alknopfler/hypershift/cmd/infra/aws"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "infra",
		Short:        "Commands for creating HyperShift infra resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateCommand())

	return cmd
}
