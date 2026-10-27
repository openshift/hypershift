package kubeconfig

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hypershiftkubeconfig "github.com/openshift/hypershift/cmd/kubeconfig"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "When kubeconfig create command is created, it should have 'kubeconfig' as use",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Use).To(Equal("kubeconfig"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should set long description from shared package",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Long).To(Equal(hypershiftkubeconfig.Description))
			},
		},
		{
			name: "When kubeconfig create command is created, it should silence usage on error",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.SilenceUsage).To(BeTrue())
			},
		},
		{
			name: "When kubeconfig create command is created, it should wire a RunE handler",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.RunE).ToNot(BeNil())
			},
		},
		{
			name: "When kubeconfig create command is created, it should default namespace to clusters",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				flag := cmd.Flag("namespace")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("clusters"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should register name flag with empty default",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				flag := cmd.Flag("name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(BeEmpty())
			},
		},
		{
			name: "When kubeconfig create command is created, it should default port-forward to false",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				flag := cmd.Flag("port-forward")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("false"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should register exactly the expected flags",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				expectedFlags := []string{
					"name",
					"namespace",
					"port-forward",
				}
				var actualFlags []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				sort.Strings(actualFlags)
				g.Expect(actualFlags).To(Equal(expectedFlags))
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

// renderCall records the arguments the RunE handler forwarded to Render.
type renderCall struct {
	called      bool
	ctx         context.Context
	namespace   string
	name        string
	portForward bool
}

// execute runs the command with the given CLI arguments against a stubbed
// render, and returns the recorded call, whatever the handler wrote to its
// error writer, and the error the command returned.
func execute(t *testing.T, ctx context.Context, renderErr error, args ...string) (*renderCall, string, error) {
	t.Helper()

	call := &renderCall{}
	render := func(ctx context.Context, namespace string, name string, portForward bool) error {
		call.called = true
		call.ctx = ctx
		call.namespace = namespace
		call.name = name
		call.portForward = portForward
		return renderErr
	}

	var errOut bytes.Buffer
	cmd := newCreateCommand(render, &errOut)
	cmd.SetArgs(args)
	// Cobra prints its own "Error: ..." copy for a returned error; discard it
	// so only the handler's own output lands in errOut.
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.ExecuteContext(ctx)
	return call, errOut.String(), err
}

func TestNewCreateCommandRunE(t *testing.T) {
	t.Parallel()

	t.Run("When no flags are set, it should render with the default namespace and no name", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, errOut, err := execute(t, context.Background(), nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(call.called).To(BeTrue())
		g.Expect(call.namespace).To(Equal("clusters"))
		// An empty name means "render a context for every HostedCluster".
		g.Expect(call.name).To(BeEmpty())
		g.Expect(call.portForward).To(BeFalse())
		g.Expect(errOut).To(BeEmpty())
	})

	t.Run("When flags are set, it should forward the parsed values to render", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, errOut, err := execute(t, context.Background(), nil,
			"--namespace", "local-cluster", "--name", "my-hosted-cluster", "--port-forward")

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(call.called).To(BeTrue())
		g.Expect(call.namespace).To(Equal("local-cluster"))
		g.Expect(call.name).To(Equal("my-hosted-cluster"))
		g.Expect(call.portForward).To(BeTrue())
		g.Expect(errOut).To(BeEmpty())
	})

	t.Run("When port-forward is set to false explicitly, it should forward false to render", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, _, err := execute(t, context.Background(), nil, "--port-forward=false")

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(call.portForward).To(BeFalse())
	})

	t.Run("When the command has a context, it should pass it through to render", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		type ctxKey struct{}
		ctx := context.WithValue(context.Background(), ctxKey{}, "sentinel")

		call, _, err := execute(t, ctx, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(call.ctx).ToNot(BeNil())
		// Render is a long-running call against the management cluster, so it
		// must receive the cancellable command context rather than a fresh one.
		g.Expect(call.ctx.Value(ctxKey{})).To(Equal("sentinel"))
	})

	t.Run("When render fails, it should return the error and report it", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		renderErr := errors.New("cluster doesn't report a kubeconfig")
		call, errOut, err := execute(t, context.Background(), renderErr, "--name", "my-hosted-cluster")

		g.Expect(call.called).To(BeTrue())
		// The error must be returned, not just printed, so the CLI exits non-zero.
		g.Expect(err).To(MatchError(renderErr))
		g.Expect(errOut).To(Equal("Error: cluster doesn't report a kubeconfig\n"))
	})

	t.Run("When an unknown flag is passed, it should fail without rendering", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, errOut, err := execute(t, context.Background(), nil, "--not-a-flag")

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unknown flag"))
		g.Expect(call.called).To(BeFalse())
		g.Expect(errOut).To(BeEmpty())
	})
}
