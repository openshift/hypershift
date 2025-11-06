package plugins

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"
)

func TestLoadPluginsIntoCreateCommand(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: create mock plugins
	enabledPlugin := &mockPlugin{
		name:        "enabled-plugin",
		description: "Enabled Plugin",
		enabled:     true,
	}
	disabledPlugin := &mockPlugin{
		name:        "disabled-plugin",
		description: "Disabled Plugin",
		enabled:     false,
	}

	registeredPlugins = []Plugin{enabledPlugin, disabledPlugin}

	// Create mock create command
	createCmd := &cobra.Command{
		Use: "create",
	}

	// Test: load plugins
	LoadPluginsIntoCreateCommand(createCmd)

	// Verify that only the enabled plugin registered commands
	g.Expect(enabledPlugin.commandsRegistered).To(BeTrue())
	g.Expect(disabledPlugin.commandsRegistered).To(BeFalse())
}

func TestLoadPluginsIntoCreateCommand_NoEnabledPlugins(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: only disabled plugins
	disabledPlugin1 := &mockPlugin{name: "disabled1", enabled: false}
	disabledPlugin2 := &mockPlugin{name: "disabled2", enabled: false}

	registeredPlugins = []Plugin{disabledPlugin1, disabledPlugin2}

	// Create mock create command
	createCmd := &cobra.Command{Use: "create"}

	// Test: load plugins
	LoadPluginsIntoCreateCommand(createCmd)

	// Verify that no plugin registered commands
	g.Expect(disabledPlugin1.commandsRegistered).To(BeFalse())
	g.Expect(disabledPlugin2.commandsRegistered).To(BeFalse())
}

func TestLoadPluginsIntoCreateCommand_EmptyRegistry(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	// Create mock create command
	createCmd := &cobra.Command{Use: "create"}

	// Test: load plugins (should not fail)
	g.Expect(func() {
		LoadPluginsIntoCreateCommand(createCmd)
	}).NotTo(Panic())
}

func TestGetPluginStatus(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: create plugins with different states
	registeredPlugins = []Plugin{
		&mockPlugin{
			name:        "enabled-plugin",
			description: "This is an enabled plugin",
			enabled:     true,
		},
		&mockPlugin{
			name:        "disabled-plugin",
			description: "This is a disabled plugin",
			enabled:     false,
		},
	}

	status := GetPluginStatus()

	g.Expect(status).To(HaveLen(2))

	// Verificar el primer plugin (enabled)
	g.Expect(status[0].Name).To(Equal("enabled-plugin"))
	g.Expect(status[0].Description).To(Equal("This is an enabled plugin"))
	g.Expect(status[0].Enabled).To(BeTrue())

	// Verificar el segundo plugin (disabled)
	g.Expect(status[1].Name).To(Equal("disabled-plugin"))
	g.Expect(status[1].Description).To(Equal("This is a disabled plugin"))
	g.Expect(status[1].Enabled).To(BeFalse())
}

func TestGetPluginStatus_EmptyRegistry(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	status := GetPluginStatus()
	g.Expect(status).To(BeEmpty())
}
