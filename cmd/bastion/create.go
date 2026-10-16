package bastion

import (
	"github.com/openshift/hypershift/cmd/bastion/aws"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCreateCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "bastion",
		Short:        "Commands for creating bastion instances",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateCommand(clientProviders...))

	return cmd
}
