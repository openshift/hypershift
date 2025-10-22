package dr

import (
	"github.com/openshift/hypershift/cmd/dr/backup"
	"github.com/openshift/hypershift/cmd/dr/restore"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dr",
		Short: "Commands for disaster recovery operations",
		Long: `Commands for disaster recovery operations in HyperShift.

This domain provides tools for backing up and restoring HyperShift hosted clusters
using OADP (OpenShift API for Data Protection) with platform-aware resource selection.

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
	}

	cmd.AddCommand(backup.NewCreateCommand())
	cmd.AddCommand(restore.NewCreateCommand())

	return cmd
}
