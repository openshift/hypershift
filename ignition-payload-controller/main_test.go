package ignitionpayloadcontroller

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewStartCommand(t *testing.T) {
	g := NewWithT(t)
	cmd := NewStartCommand()
	g.Expect(cmd).ToNot(BeNil())
	g.Expect(cmd.Use).To(Equal("ignition-payload-controller"))
	// Flags the command exposes for deployment configuration.
	g.Expect(cmd.Flags().Lookup("work-dir")).ToNot(BeNil())
	g.Expect(cmd.Flags().Lookup("platform")).ToNot(BeNil())
	g.Expect(cmd.Flags().Lookup("feature-gate-manifest")).ToNot(BeNil())
}
