package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/config"

	"github.com/spf13/cobra"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "When IAM create command is created, it should have 'azure' as use",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When IAM create command is created, it should silence usage and wire RunE",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.SilenceUsage).To(BeTrue())
				g.Expect(cmd.RunE).ToNot(BeNil())
			},
		},
		{
			name: "When IAM create command is created, it should mark name as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark infra-id as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("infra-id")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark azure-creds as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("azure-creds")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark resource-group-name as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("resource-group-name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark oidc-issuer-url as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("oidc-issuer-url")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should mark output-file as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("output-file")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When IAM create command is created, it should set location to default",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("location")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureLocation))
			},
		},
		{
			name: "When IAM create command is created, it should set cloud to default",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				flag := cmd.Flag("cloud")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal(config.DefaultAzureCloud))
			},
		},
		{
			name: "When RunE runs with missing required options, it should return a validation error",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				err := cmd.RunE(cmd, nil)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("name is required"))
			},
		},
		{
			name: "When RunE runs past validation with an unreadable azure-creds file, it should return an error from Run",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Flags().Set("name", "test-cluster")).To(Succeed())
				g.Expect(cmd.Flags().Set("infra-id", "test-infra")).To(Succeed())
				g.Expect(cmd.Flags().Set("azure-creds", "/nonexistent/azure-creds")).To(Succeed())
				g.Expect(cmd.Flags().Set("resource-group-name", "test-rg")).To(Succeed())
				g.Expect(cmd.Flags().Set("oidc-issuer-url", "https://example.com/oidc")).To(Succeed())
				g.Expect(cmd.Flags().Set("output-file", "/dev/null")).To(Succeed())

				err := cmd.RunE(cmd, nil)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to setup Azure credentials"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewCreateCommand()
			tt.test(t, cmd)
		})
	}
}
