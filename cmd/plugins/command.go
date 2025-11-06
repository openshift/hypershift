package plugins

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// NewPluginsCommand creates the plugins management command
func NewPluginsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage HyperShift CLI plugins",
		Long:  `Enable, disable, and list available HyperShift CLI plugins.`,
	}

	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newEnableCommand())
	cmd.AddCommand(newDisableCommand())

	return cmd
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all available plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listPlugins()
		},
	}
}

func newEnableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <plugin-name>",
		Short: "Enable a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return enablePlugin(args[0])
		},
	}
}

func newDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <plugin-name>",
		Short: "Disable a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return disablePlugin(args[0])
		},
	}
}

func listPlugins() error {
	status := GetPluginStatus()

	if len(status) == 0 {
		fmt.Println("No plugins available")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tDESCRIPTION")

	for _, plugin := range status {
		status := "disabled"
		if plugin.Enabled {
			status = "enabled"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", plugin.Name, status, plugin.Description)
	}

	return w.Flush()
}

// verifyPluginExists checks if a plugin with the given name is registered
func verifyPluginExists(pluginName string) error {
	for _, plugin := range GetAllPlugins() {
		if plugin.Name() == pluginName {
			return nil
		}
	}
	return fmt.Errorf("plugin '%s' not found", pluginName)
}

func enablePlugin(pluginName string) error {
	if err := verifyPluginExists(pluginName); err != nil {
		return err
	}

	if err := SetPluginEnabled(pluginName, true); err != nil {
		return fmt.Errorf("failed to enable plugin '%s': %w", pluginName, err)
	}

	fmt.Printf("Plugin '%s' enabled successfully\n", pluginName)
	return nil
}

func disablePlugin(pluginName string) error {
	if err := verifyPluginExists(pluginName); err != nil {
		return err
	}

	if err := SetPluginEnabled(pluginName, false); err != nil {
		return fmt.Errorf("failed to disable plugin '%s': %w", pluginName, err)
	}

	fmt.Printf("Plugin '%s' disabled successfully\n", pluginName)
	return nil
}

// ListPlugins lists all available plugins with their status
func ListPlugins() error {
	return listPlugins()
}

// EnablePlugin enables a plugin by name and saves the configuration
func EnablePlugin(pluginName string) error {
	return enablePlugin(pluginName)
}

// DisablePlugin disables a plugin by name and saves the configuration
func DisablePlugin(pluginName string) error {
	return disablePlugin(pluginName)
}
