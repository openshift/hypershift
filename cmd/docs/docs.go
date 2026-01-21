package docs

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the docs command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "docs",
		Short:        "Generate CLI documentation",
		Long:         "Commands for generating CLI flag documentation in various formats.",
		SilenceUsage: true,
	}

	cmd.AddCommand(NewGenerateCommand())

	return cmd
}
