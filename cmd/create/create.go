package create

import (
	"github.com/spf13/cobra"

	"openshift.io/hypershift/cmd/cluster"
	"openshift.io/hypershift/cmd/infra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Commands for creating HyperShift resources",
	}

	cmd.AddCommand(cluster.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateCommand())

	return cmd
}
