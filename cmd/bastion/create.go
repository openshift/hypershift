package bastion

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/bastion/aws"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "bastion",
		Short:        "Commands for creating bastion instances",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateCommand())

	return cmd
}
