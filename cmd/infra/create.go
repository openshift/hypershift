package infra

import (
	"github.com/spf13/cobra"

	"openshift.io/hypershift/cmd/infra/aws"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "Commands for creating HyperShift infra resources",
	}

	cmd.AddCommand(aws.NewCreateCommand())

	return cmd
}
