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
			name: "When IAM create command is created, it should have 'azure' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When IAM create command is created, it should mark name as required",
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
			name: "When IAM create command is created, it should mark infra-id as required",
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
			name: "When IAM create command is created, it should mark azure-creds as required",
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
			name: "When IAM create command is created, it should mark resource-group-name as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("resource-group-name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark oidc-issuer-url as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("oidc-issuer-url")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark output-file as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("output-file")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should set location to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("location")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureLocation))
			},
		},
		{
			name: "When IAM create command is created, it should set cloud to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

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
