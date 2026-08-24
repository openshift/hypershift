package dump

import (
	clusterdump "github.com/openshift/hypershift/cmd/cluster/dump"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "dump",
		Short:        "Commands for dumping resources for debugging",
		SilenceUsage: true,
	}

	cmd.AddCommand(clusterdump.NewDumpCommand())

	return cmd
}
