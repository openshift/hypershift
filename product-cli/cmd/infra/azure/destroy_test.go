package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/config"

	"github.com/spf13/cobra"
)

func TestNewDestroyCommand(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When infra destroy command is created, it should have 'azure' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When infra destroy command is created, it should mark infra-id as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("infra-id")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When infra destroy command is created, it should mark azure-creds as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("azure-creds")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When infra destroy command is created, it should mark name as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When infra destroy command is created, it should set location to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("location")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureLocation))
			},
		},
		{
			name: "When infra destroy command is created, it should set cloud to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("cloud")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureCloud))
			},
		},
		{
			name: "When infra destroy command is created, it should default preserve-resource-group to false",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("preserve-resource-group")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("false"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}
