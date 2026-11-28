package cmd

import (
	"github.com/spf13/cobra"
)

// NewScanCommand creates the scan parent command with subcommands for different
// scan modes: tags, deep, and detect.
func NewScanCommand(globals *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Discover and report on HyperShift AWS infrastructure",
		Long: `Scan AWS regions for HyperShift-related infrastructure resources.

Three scan modes are available:
  tags   - Fast tag-based discovery via Resource Groups Tagging API
  deep   - Tag scan + supplemental API calls for untagged resources
  detect - Cross-reference with management cluster HostedClusters`,
	}

	cmd.AddCommand(NewScanTagsCommand(globals))
	cmd.AddCommand(NewScanDeepCommand(globals))
	cmd.AddCommand(NewScanDetectCommand(globals))

	return cmd
}
