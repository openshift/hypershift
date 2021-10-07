package bastion

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/bastion/aws"
)

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "bastion",
		Short:        "Commands for destroying bastion instances",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewDestroyCommand())

	return cmd
}
