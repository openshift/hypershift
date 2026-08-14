package infra

import (
	"github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/infra/gcp"

	"github.com/spf13/cobra"
)

func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "iam",
		Short:        "Commands for destroying HyperShift IAM resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewDestroyIAMCommand())
	cmd.AddCommand(azure.NewDestroyIAMCommand())
	cmd.AddCommand(gcp.NewDestroyIAMCommand())

	return cmd
}
