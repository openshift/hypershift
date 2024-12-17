package infra

import (
	"github.com/openshift/hypershift/cmd/infra/aws"

	"github.com/spf13/cobra"
)

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "iam",
		Short:        "Commands for creating HyperShift IAM resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateIAMCommand())
	cmd.AddCommand(aws.NewCreateCLIRoleCommand())

	return cmd
}
