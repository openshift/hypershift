package dump

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/cluster"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "dump",
		Short:        "Commands for dumping resources for debugging",
		SilenceUsage: true,
	}

	cmd.AddCommand(cluster.NewDumpCommand())

	return cmd
}
