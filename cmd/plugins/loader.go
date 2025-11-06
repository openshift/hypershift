package plugins

import (
	"github.com/openshift/hypershift/cmd/plugins/oadp"

	"github.com/spf13/cobra"
)

func init() {
	// Register available plugins
	RegisterPlugin(oadp.NewPluginWithConfigChecker(IsPluginEnabled))
}

// LoadPluginsIntoCreateCommand loads all enabled plugins into the create command
func LoadPluginsIntoCreateCommand(createCmd *cobra.Command) {
	LoadPluginsIntoCommand(createCmd, "create")
}

// LoadPluginsIntoCommand loads all enabled plugins into the specified command
func LoadPluginsIntoCommand(targetCommand *cobra.Command, targetCommandName string) {
	enabledPlugins := GetEnabledPlugins()
	for _, plugin := range enabledPlugins {
		plugin.RegisterCommandInto(targetCommand, targetCommandName)
	}
}

// GetNewCommandsFromPlugins returns new top-level commands from all enabled plugins
func GetNewCommandsFromPlugins() []*cobra.Command {
	var newCommands []*cobra.Command
	enabledPlugins := GetEnabledPlugins()

	for _, plugin := range enabledPlugins {
		pluginNewCommands := plugin.RegisterNewCommand()
		if pluginNewCommands != nil {
			newCommands = append(newCommands, pluginNewCommands...)
		}
	}

	return newCommands
}

// GetPluginStatus returns information about all plugins
func GetPluginStatus() []PluginStatus {
	var status []PluginStatus
	for _, plugin := range GetAllPlugins() {
		status = append(status, PluginStatus{
			Name:        plugin.Name(),
			Description: plugin.Description(),
			Enabled:     plugin.IsEnabled(),
		})
	}
	return status
}
