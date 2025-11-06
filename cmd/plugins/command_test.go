package plugins

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewPluginsCommand(t *testing.T) {
	g := NewWithT(t)

	cmd := NewPluginsCommand()

	g.Expect(cmd.Use).To(Equal("plugins"))
	g.Expect(cmd.Short).To(Equal("Manage HyperShift CLI plugins"))
	g.Expect(cmd.Long).To(ContainSubstring("Enable, disable, and list available HyperShift CLI plugins"))

	// Verify subcommands
	subcommands := cmd.Commands()
	g.Expect(subcommands).To(HaveLen(3))

	subcommandNames := make([]string, len(subcommands))
	for i, subcmd := range subcommands {
		subcommandNames[i] = subcmd.Use
	}

	g.Expect(subcommandNames).To(ContainElement("list"))
	g.Expect(subcommandNames).To(ContainElement("enable <plugin-name>"))
	g.Expect(subcommandNames).To(ContainElement("disable <plugin-name>"))
}

func TestListPlugins_NoPlugins(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	err := listPlugins()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestEnablePlugin_Success(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: temporary directory for configuration
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	originalGetConfigPath := getConfigPath
	defer func() {
		getConfigPath = func() string { return originalGetConfigPath() }
	}()

	getConfigPath = func() string { return configPath }

	// Reset global config
	config = nil

	// Setup: add test plugin
	registeredPlugins = []Plugin{
		&mockPlugin{name: "test-plugin", description: "Test Plugin"},
	}

	err := enablePlugin("test-plugin")
	g.Expect(err).NotTo(HaveOccurred())

	// Verify that the plugin is enabled in configuration
	g.Expect(IsPluginEnabled("test-plugin")).To(BeTrue())
}

func TestEnablePlugin_PluginNotFound(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	err := enablePlugin("nonexistent-plugin")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("plugin 'nonexistent-plugin' not found"))
}

func TestDisablePlugin_Success(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: temporary directory for configuration
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	originalGetConfigPath := getConfigPath
	defer func() {
		getConfigPath = func() string { return originalGetConfigPath() }
	}()

	getConfigPath = func() string { return configPath }

	// Reset global config
	config = nil

	// Setup: add test plugin
	registeredPlugins = []Plugin{
		&mockPlugin{name: "test-plugin", description: "Test Plugin"},
	}

	// First enable the plugin
	err := SetPluginEnabled("test-plugin", true)
	g.Expect(err).NotTo(HaveOccurred())

	err = disablePlugin("test-plugin")
	g.Expect(err).NotTo(HaveOccurred())

	// Verify that the plugin is disabled in configuration
	g.Expect(IsPluginEnabled("test-plugin")).To(BeFalse())
}

func TestDisablePlugin_PluginNotFound(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	err := disablePlugin("nonexistent-plugin")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("plugin 'nonexistent-plugin' not found"))
}

func TestVerifyPluginExists_PluginExists(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: add test plugin
	registeredPlugins = []Plugin{
		&mockPlugin{name: "test-plugin", description: "Test Plugin"},
	}

	err := verifyPluginExists("test-plugin")
	g.Expect(err).NotTo(HaveOccurred())
}

func TestVerifyPluginExists_PluginNotFound(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: empty registry
	registeredPlugins = []Plugin{}

	err := verifyPluginExists("nonexistent-plugin")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(Equal("plugin 'nonexistent-plugin' not found"))
}

func TestVerifyPluginExists_MultiplePlugins(t *testing.T) {
	g := NewWithT(t)

	// Backup original state
	originalPlugins := make([]Plugin, len(registeredPlugins))
	copy(originalPlugins, registeredPlugins)
	defer func() {
		registeredPlugins = originalPlugins
	}()

	// Setup: add multiple test plugins
	registeredPlugins = []Plugin{
		&mockPlugin{name: "plugin-one", description: "First Plugin"},
		&mockPlugin{name: "plugin-two", description: "Second Plugin"},
		&mockPlugin{name: "plugin-three", description: "Third Plugin"},
	}

	// Test existing plugin
	err := verifyPluginExists("plugin-two")
	g.Expect(err).NotTo(HaveOccurred())

	// Test non-existing plugin
	err = verifyPluginExists("plugin-four")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(Equal("plugin 'plugin-four' not found"))
}
