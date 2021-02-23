package infra

import (
	"github.com/spf13/cobra"

	"openshift.io/hypershift/cmd/infra/aws"
)

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "iam",
		Short: "Commands for creating HyperShift IAM resources",
	}

	cmd.AddCommand(aws.NewCreateIAMCommand())

	return cmd
}
