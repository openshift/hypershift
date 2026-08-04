package version

import (
	"bytes"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/supportedversion"
)

func TestNewVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When commit-only flag is not set, it should include client version in output",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()
				cmd.SetArgs([]string{"--client-only"})

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				err := cmd.Execute()
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(buf.String()).To(HavePrefix("Client Version:"))
			},
		},
		{
			name: "When commit-only flag is set, it should print only the revision",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()
				cmd.SetArgs([]string{"--commit-only"})

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				err := cmd.Execute()
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(buf.String()).To(Equal(fmt.Sprintf("%s\n", supportedversion.GetRevision())))
			},
		},
		{
			name: "When client-only flag is set, it should print only the client version",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()
				cmd.SetArgs([]string{"--client-only"})

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				err := cmd.Execute()
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(buf.String()).To(Equal(fmt.Sprintf("Client Version: %s\n", supportedversion.String())))
			},
		},
		{
			name: "When no flags are set, it should print client version and fail gracefully without a server",
			test: func(t *testing.T) {
				g := NewWithT(t)

				t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
				t.Setenv("KUBERNETES_SERVICE_HOST", "")

				cmd := NewVersionCommand()
				cmd.SetArgs([]string{})

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				err := cmd.Execute()
				g.Expect(err).ToNot(HaveOccurred())
				output := buf.String()
				g.Expect(output).To(HavePrefix("Client Version:"))
				g.Expect(output).To(ContainSubstring("failed to connect to server:"))
			},
		},
		{
			name: "When version command is created, it should default namespace to hypershift",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()

				flag := cmd.Flag("namespace")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("hypershift"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}
