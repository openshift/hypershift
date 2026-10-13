package infra

import (
	"github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/infra/gcp"

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
	cmd.AddCommand(azure.NewCreateIAMCommand())
	cmd.AddCommand(gcp.NewCreateIAMCommand())

	return cmd
}
