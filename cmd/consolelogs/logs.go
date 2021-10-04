package consolelogs

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/consolelogs/aws"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "console-logs",
		Short:        "Get machine instance console logs",
		SilenceUsage: true,
	}

	cmd.AddCommand(aws.NewCommand())
	return cmd
}
