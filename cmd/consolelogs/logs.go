package consolelogs

import (
	"github.com/openshift/hypershift/cmd/consolelogs/aws"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "console-logs",
		Short:        "Get machine instance console logs",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCommand(clientProviders...))
	return cmd
}
