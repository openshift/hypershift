package ignitionpayloadserver

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewStartCommand(t *testing.T) {
	g := NewWithT(t)
	cmd := NewStartCommand()
	g.Expect(cmd).ToNot(BeNil())
	g.Expect(cmd.Use).To(Equal("ignition-payload-server"))
	g.Expect(cmd.Flags().Lookup("addr")).ToNot(BeNil())
	g.Expect(cmd.Flags().Lookup("cert-file")).ToNot(BeNil())
	g.Expect(cmd.Flags().Lookup("key-file")).ToNot(BeNil())
	g.Expect(cmd.Flags().Lookup("token-verification-key-file")).ToNot(BeNil())
}
