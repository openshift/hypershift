package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/config"

	"github.com/spf13/cobra"
)

func TestNewCreateCommand(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When infra create command is created, it should have 'azure' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When infra create command is created, it should mark infra-id as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("infra-id")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When infra create command is created, it should mark azure-creds as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("azure-creds")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When infra create command is created, it should mark name as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When infra create command is created, it should set location to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("location")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureLocation))
			},
		},
		{
			name: "When infra create command is created, it should set cloud to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("cloud")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureCloud))
			},
		},
		{
			name: "When infra create command is created, it should register product-specific flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				g.Expect(cmd.Flag("base-domain")).ToNot(BeNil())
				g.Expect(cmd.Flag("resource-group-name")).ToNot(BeNil())
				g.Expect(cmd.Flag("resource-group-tags")).ToNot(BeNil())
				g.Expect(cmd.Flag("vnet-id")).ToNot(BeNil())
				g.Expect(cmd.Flag("subnet-id")).ToNot(BeNil())
				g.Expect(cmd.Flag("network-security-group-id")).ToNot(BeNil())
				g.Expect(cmd.Flag("workload-identities-file")).ToNot(BeNil())
				g.Expect(cmd.Flag("assign-identity-roles")).ToNot(BeNil())
				g.Expect(cmd.Flag("output-file")).ToNot(BeNil())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}
