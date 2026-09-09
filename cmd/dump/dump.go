package dump

import (
	clusterdump "github.com/openshift/hypershift/cmd/cluster/dump"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "dump",
		Short:        "Commands for dumping resources for debugging",
		SilenceUsage: true,
	}

	clientProvider := util.ResolveClientProvider(clientProviders...)
	cmd.AddCommand(clusterdump.NewDumpCommand(clusterdump.DumpClusterWithRetry, clientProvider))

	return cmd
}
