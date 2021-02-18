package create

import (
	"github.com/spf13/cobra"

	"openshift.io/hypershift/cmd/create/cluster"
	"openshift.io/hypershift/cmd/create/infra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Commands for creating HyperShift resources",
	}

	cmd.AddCommand(cluster.NewCommand())
	cmd.AddCommand(infra.NewCommand())

	return cmd
}
