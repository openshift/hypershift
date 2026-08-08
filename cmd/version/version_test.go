package version

import (
	"bytes"
	"fmt"
	"io"
	"os"
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
			name: "When version command is created, it should have 'version' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()
				g.Expect(cmd.Use).To(Equal("version"))
			},
		},
		{
			name: "When version command is created, it should register commit-only flag with false default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()

				flag := cmd.Flag("commit-only")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("false"))
			},
		},
		{
			name: "When version command is created, it should register client-only flag with false default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()

				flag := cmd.Flag("client-only")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("false"))
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
		{
			name: "When commit-only flag is set, it should print only the revision",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()
				cmd.SetArgs([]string{"--commit-only"})

				output := captureStdout(t, func() {
					_ = cmd.Execute()
				})

				g.Expect(output).To(Equal(fmt.Sprintf("%s\n", supportedversion.GetRevision())))
			},
		},
		{
			name: "When client-only flag is set, it should print only the client version",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewVersionCommand()
				cmd.SetArgs([]string{"--client-only"})

				output := captureStdout(t, func() {
					_ = cmd.Execute()
				})

				g.Expect(output).To(Equal(fmt.Sprintf("Client Version: %s\n", supportedversion.String())))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read captured output: %v", err)
	}

	return buf.String()
}
