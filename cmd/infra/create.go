package infra

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/infra/azure"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "infra",
		Short:        "Commands for creating HyperShift infra resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateCommand())
	cmd.AddCommand(azure.NewCreateCommand())

	return cmd
}
