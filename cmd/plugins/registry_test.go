package plugins

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"
)

// Mock plugin for tests
type mockPlugin struct {
	name               string
	description        string
	enabled            bool
	commandsRegistered bool
}

func (m *mockPlugin) Name() string {
	return m.name
}

func (m *mockPlugin) Description() string {
	return m.description
}

func (m *mockPlugin) IsEnabled() bool {
	return m.enabled
}

func (m *mockPlugin) RegisterCommandInto(targetCommand *cobra.Command, targetCommandName string) {
	m.commandsRegistered = true
	// In a real test, we would add mock commands here
}

func (m *mockPlugin) RegisterNewCommand() []*cobra.Command {
	// Mock plugin doesn't register new top-level commands
	return nil
}

func TestRegisterPlugin(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Reset registry for the test
	registeredPlugins = []Plugin{}

	plugin1 := &mockPlugin{name: "test-plugin-1", description: "Test Plugin 1"}
	plugin2 := &mockPlugin{name: "test-plugin-2", description: "Test Plugin 2"}

	RegisterPlugin(plugin1)
	g.Expect(registeredPlugins).To(HaveLen(1))
	g.Expect(registeredPlugins[0].Name()).To(Equal("test-plugin-1"))

	RegisterPlugin(plugin2)
	g.Expect(registeredPlugins).To(HaveLen(2))
	g.Expect(registeredPlugins[1].Name()).To(Equal("test-plugin-2"))
}

func TestGetAllPlugins(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: add test plugins
	registeredPlugins = []Plugin{
		&mockPlugin{name: "plugin1", description: "Plugin 1"},
		&mockPlugin{name: "plugin2", description: "Plugin 2"},
	}

	plugins := GetAllPlugins()
	g.Expect(plugins).To(HaveLen(2))
	g.Expect(plugins[0].Name()).To(Equal("plugin1"))
	g.Expect(plugins[1].Name()).To(Equal("plugin2"))
}

func TestGetEnabledPlugins(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: add test plugins
	registeredPlugins = []Plugin{
		&mockPlugin{name: "enabled-plugin", description: "Enabled Plugin", enabled: true},
		&mockPlugin{name: "disabled-plugin", description: "Disabled Plugin", enabled: false},
		&mockPlugin{name: "another-enabled", description: "Another Enabled Plugin", enabled: true},
	}

	enabledPlugins := GetEnabledPlugins()
	g.Expect(enabledPlugins).To(HaveLen(2))

	// Verify that only enabled plugins are included
	enabledNames := make([]string, len(enabledPlugins))
	for i, plugin := range enabledPlugins {
		enabledNames[i] = plugin.Name()
		g.Expect(plugin.IsEnabled()).To(BeTrue())
	}

	g.Expect(enabledNames).To(ContainElement("enabled-plugin"))
	g.Expect(enabledNames).To(ContainElement("another-enabled"))
	g.Expect(enabledNames).NotTo(ContainElement("disabled-plugin"))
}

func TestGetEnabledPlugins_EmptyWhenAllDisabled(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: add only disabled plugins
	registeredPlugins = []Plugin{
		&mockPlugin{name: "disabled1", description: "Disabled Plugin 1", enabled: false},
		&mockPlugin{name: "disabled2", description: "Disabled Plugin 2", enabled: false},
	}

	enabledPlugins := GetEnabledPlugins()
	g.Expect(enabledPlugins).To(BeEmpty())
}

func TestGetEnabledPlugins_EmptyRegistry(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	enabledPlugins := GetEnabledPlugins()
	g.Expect(enabledPlugins).To(BeEmpty())
}
