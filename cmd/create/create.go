package create

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/cluster"
	"github.com/openshift/hypershift/cmd/infra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Commands for creating HyperShift resources",
	}

	cmd.AddCommand(cluster.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateIAMCommand())

	return cmd
}
