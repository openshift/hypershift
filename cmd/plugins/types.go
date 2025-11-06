package plugins

import (
	"github.com/spf13/cobra"
)

// Plugin represents a CLI plugin that can be enabled/disabled
type Plugin interface {
	Name() string
	Description() string
	IsEnabled() bool

	// RegisterCommandInto allows plugins to register subcommands into existing commands.
	// For example, registering 'backup' into the 'create' command to create 'create backup'.
	// The targetCommandName indicates which command to register into (e.g., "create").
	RegisterCommandInto(targetCommand *cobra.Command, targetCommandName string)

	// RegisterNewCommand allows plugins to register entirely new top-level commands.
	// For example, registering a 'manage' command that becomes a new root command.
	// Returns the new command(s) that should be added to the root CLI.
	RegisterNewCommand() []*cobra.Command
}

// PluginStatus represents the status of a plugin
type PluginStatus struct {
	Name        string
	Description string
	Enabled     bool
}
