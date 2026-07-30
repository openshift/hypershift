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
			name: "When IAM destroy command is created, it should have 'azure' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When IAM destroy command is created, it should mark name as required",
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
			name: "When IAM destroy command is created, it should mark infra-id as required",
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
			name: "When IAM destroy command is created, it should mark workload-identities-file as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("workload-identities-file")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM destroy command is created, it should mark azure-creds as required",
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
			name: "When IAM destroy command is created, it should mark resource-group-name as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("resource-group-name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM destroy command is created, it should set cloud to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewDestroyCommand()

				flag := cmd.Flag("cloud")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureCloud))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}
