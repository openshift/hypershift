package get

import (
	"github.com/openshift/hypershift/cmd/oadp"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get",
		Short:        "Commands for listing HyperShift resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(oadp.NewGetBackupsCommand())
	cmd.AddCommand(oadp.NewGetRestoresCommand())
	cmd.AddCommand(oadp.NewGetSchedulesCommand())

	return cmd
}
