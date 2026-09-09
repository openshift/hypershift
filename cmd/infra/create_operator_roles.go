package infra

import (
	"github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCreateOperatorRolesCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "operator-roles",
		Short:        "Commands for creating IAM roles for the HyperShift operator",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCreateOperatorRolesCommand(clientProviders...))

	return cmd
}
