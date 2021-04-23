package infra

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/infra/aws"
)

func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "iam",
		Short:        "Commands for destroying HyperShift IAM resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewDestroyIAMCommand())

	return cmd
}
