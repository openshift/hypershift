package create

import (
	"github.com/spf13/cobra"

	"openshift.io/hypershift/cmd/create/cluster"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Commands for creating HyperShift resources",
	}

	cmd.AddCommand(cluster.NewCommand())

	return cmd
}
