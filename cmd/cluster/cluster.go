package cluster

import (
	"github.com/spf13/cobra"

	"openshift.io/hypershift/cmd/cluster/example"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Commands for working with HyperShift clusters",
	}

	cmd.AddCommand(example.NewCommand())

	return cmd
}
