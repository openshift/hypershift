package infra

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/infra/aws"
)

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "iam",
		Short:        "Commands for creating HyperShift IAM resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateIAMCommand())

	return cmd
}
