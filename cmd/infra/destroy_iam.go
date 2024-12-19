package infra

import (
	"github.com/openshift/hypershift/cmd/infra/aws"

	"github.com/spf13/cobra"
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
