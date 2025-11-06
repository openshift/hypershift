package oadp

import (
	"github.com/spf13/cobra"
)

// ConfigChecker is a function type that checks if a plugin is enabled
type ConfigChecker func(pluginName string) bool

// Plugin represents the OADP plugin functionality
type Plugin struct {
	isEnabledFunc ConfigChecker
}

func (p *Plugin) Name() string {
	return "oadp"
}

func (p *Plugin) Description() string {
	return "OADP backup functionality for hosted clusters"
}

func (p *Plugin) IsEnabled() bool {
	if p.isEnabledFunc != nil {
		return p.isEnabledFunc("oadp")
	}
	return false
}

func (p *Plugin) RegisterCommandInto(targetCommand *cobra.Command, targetCommandName string) {
	// Register commands into the create command only
	// TODO: In the next step we will add the backup command here
	// if targetCommandName == "create" {
	//     targetCommand.AddCommand(backup.NewCreateCommand())
	// }
}

func (p *Plugin) RegisterNewCommand() []*cobra.Command {
	// OADP plugin doesn't register new top-level commands
	return nil
}

// NewPlugin returns a new OADP plugin instance
func NewPlugin() *Plugin {
	return &Plugin{}
}

// NewPluginWithConfigChecker returns a new OADP plugin instance with a config checker function
func NewPluginWithConfigChecker(configChecker ConfigChecker) *Plugin {
	return &Plugin{
		isEnabledFunc: configChecker,
	}
}
