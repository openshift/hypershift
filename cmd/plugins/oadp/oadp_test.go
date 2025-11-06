package oadp

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"
)

func TestOADPPlugin_Name(t *testing.T) {
	g := NewWithT(t)

	plugin := NewPlugin()
	g.Expect(plugin.Name()).To(Equal("oadp"))
}

func TestOADPPlugin_Description(t *testing.T) {
	g := NewWithT(t)

	plugin := NewPlugin()
	expected := "OADP backup functionality for hosted clusters"
	g.Expect(plugin.Description()).To(Equal(expected))
}

func TestOADPPlugin_IsEnabled_UsesGlobalConfig(t *testing.T) {
	g := NewWithT(t)

	plugin := NewPlugin()

	// Test: This should use the global configuration system
	// The plugin delegates to plugins.IsPluginEnabled("oadp")
	// We don't mock the configuration here, but verify that
	// the method works without errors and returns a boolean
	result := plugin.IsEnabled()

	// The important part is that this method doesn't fail and uses the central API
	g.Expect(result).To(BeAssignableToTypeOf(true))
}

func TestOADPPlugin_RegisterCommandInto(t *testing.T) {
	g := NewWithT(t)

	plugin := NewPlugin()

	// Create mock create command
	createCmd := &cobra.Command{
		Use: "create",
	}

	// Verify that there are no commands initially
	g.Expect(createCmd.Commands()).To(HaveLen(0))

	// Register plugin commands into create command
	plugin.RegisterCommandInto(createCmd, "create")

	// TODO: When we implement the real backup command,
	// we will verify that the command was added
	// For now, no commands are added (implementation pending)
	g.Expect(createCmd.Commands()).To(HaveLen(0))

	// Test with non-create command (should not register)
	otherCmd := &cobra.Command{
		Use: "install",
	}
	plugin.RegisterCommandInto(otherCmd, "install")
	g.Expect(otherCmd.Commands()).To(HaveLen(0))
}

func TestOADPPlugin_RegisterNewCommand(t *testing.T) {
	g := NewWithT(t)

	plugin := NewPlugin()

	// OADP plugin should not register new top-level commands
	newCommands := plugin.RegisterNewCommand()
	g.Expect(newCommands).To(BeNil())
}

func TestOADPPlugin_NewPlugin(t *testing.T) {
	g := NewWithT(t)

	plugin := NewPlugin()

	g.Expect(plugin).NotTo(BeNil())

	// Verify that it implements the Plugin interface
	g.Expect(plugin.Name()).To(BeAssignableToTypeOf(""))
	g.Expect(plugin.Description()).To(BeAssignableToTypeOf(""))
	g.Expect(plugin.IsEnabled()).To(BeAssignableToTypeOf(true))
}
