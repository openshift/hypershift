package verify

import (
	"github.com/openshift/hypershift/cmd/oadp"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "verify",
		Short:        "Commands for verifying HyperShift resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(oadp.NewVerifyBackupCommand())

	return cmd
}
