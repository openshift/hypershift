package azure

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/support/config"

	"github.com/go-logr/logr"
	"github.com/spf13/pflag"
)

// createIAMCall records what the command handed to the create step.
type createIAMCall struct {
	called bool
	ctx    context.Context
	opts   *hypershiftazure.CreateIAMOptions
}

// runCreate builds the command with a recording create step in place of the
// real Azure call, executes it with args, and returns both what the command
// handed to that step and the error the CLI would exit with.
func runCreate(t *testing.T, ctx context.Context, createErr error, args ...string) (*createIAMCall, error) {
	t.Helper()

	call := &createIAMCall{}
	cmd := newCreateCommand(func(runCtx context.Context, opts *hypershiftazure.CreateIAMOptions, _ logr.Logger) error {
		call.called = true
		call.ctx = runCtx
		call.opts = opts
		return createErr
	})
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	return call, cmd.ExecuteContext(ctx)
}

// requiredCreateArgs is the smallest invocation that reaches Azure.
func requiredCreateArgs() []string {
	return []string{
		"--name=test-cluster",
		"--infra-id=test-cluster-fh7z2",
		"--azure-creds=/tmp/azure-creds.json",
		"--resource-group-name=test-cluster-fh7z2",
		"--oidc-issuer-url=https://oidc.example.com/test-cluster-fh7z2",
		"--output-file=/tmp/workload-identities.json",
	}
}

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	t.Run("When the command is created, it should expose only the product flag set", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// The product CLI deliberately exposes a narrower flag set than the dev
		// CLI. Binding the dev flags here would leak unsupported flags into a
		// shipped command, so the exposed set is asserted explicitly.
		var flags []string
		NewCreateCommand().Flags().VisitAll(func(f *pflag.Flag) {
			flags = append(flags, f.Name)
		})
		g.Expect(flags).To(ConsistOf(
			"azure-creds",
			"cloud",
			"enable-karpenter",
			"enable-kms",
			"infra-id",
			"location",
			"name",
			"oidc-issuer-url",
			"output-file",
			"resource-group-name",
		), "create iam azure must expose exactly the product flag set, with no dev-CLI flags leaking in")
	})

	t.Run("When all flags are provided, it should hand the parsed values to the create step and succeed", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runCreate(t, context.Background(), nil,
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=/tmp/azure-creds.json",
			"--resource-group-name=test-cluster-fh7z2",
			"--oidc-issuer-url=https://oidc.example.com/test-cluster-fh7z2",
			"--output-file=/tmp/workload-identities.json",
			"--location=westus2",
			"--cloud=AzureUSGovernmentCloud",
			"--enable-kms=true",
			"--enable-karpenter=true",
		)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with all valid flags")
		g.Expect(call.called).To(BeTrue(), "create step must run once all flags validate")
		g.Expect(call.opts.Name).To(Equal("test-cluster"), "--name should reach the create step")
		g.Expect(call.opts.InfraID).To(Equal("test-cluster-fh7z2"), "--infra-id should reach the create step")
		g.Expect(call.opts.CredentialsFile).To(Equal("/tmp/azure-creds.json"), "--azure-creds should reach the create step")
		g.Expect(call.opts.ResourceGroupName).To(Equal("test-cluster-fh7z2"), "--resource-group-name should reach the create step")
		g.Expect(call.opts.OIDCIssuerURL).To(Equal("https://oidc.example.com/test-cluster-fh7z2"), "--oidc-issuer-url should reach the create step")
		g.Expect(call.opts.OutputFile).To(Equal("/tmp/workload-identities.json"), "--output-file should reach the create step")
		g.Expect(call.opts.Location).To(Equal("westus2"), "--location should override the default location")
		g.Expect(call.opts.Cloud).To(Equal("AzureUSGovernmentCloud"), "--cloud should override the default cloud")
		g.Expect(call.opts.EnableKMS).To(BeTrue(), "--enable-kms=true should reach the create step")
		g.Expect(call.opts.EnableKarpenter).To(BeTrue(), "--enable-karpenter=true should reach the create step")
	})

	t.Run("When only the required flags are provided, it should apply the documented defaults", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runCreate(t, context.Background(), nil, requiredCreateArgs()...)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with only the required flags")
		// The defaults have to survive all the way to the Azure call, not just
		// be present as a flag default string.
		g.Expect(call.opts.Location).To(Equal(config.DefaultAzureLocation), "location should default to config.DefaultAzureLocation")
		g.Expect(call.opts.Cloud).To(Equal(config.DefaultAzureCloud), "cloud should default to config.DefaultAzureCloud")
		g.Expect(call.opts.EnableKMS).To(BeFalse(), "KMS must stay disabled unless --enable-kms is set")
		g.Expect(call.opts.EnableKarpenter).To(BeFalse(), "Karpenter must stay disabled unless --enable-karpenter is set")
	})

	t.Run("When a required flag is omitted, it should fail before creating anything", func(t *testing.T) {
		t.Parallel()

		// cmd.MarkFlagRequired reports an error for a misspelled flag name and
		// every caller discards it, so a typo there would silently drop the
		// requirement. Each flag is exercised through the command to prove the
		// requirement is actually enforced.
		for _, missing := range []string{
			"name",
			"infra-id",
			"azure-creds",
			"resource-group-name",
			"oidc-issuer-url",
			"output-file",
		} {
			t.Run(missing, func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				var args []string
				for _, arg := range requiredCreateArgs() {
					if !strings.HasPrefix(arg, "--"+missing+"=") {
						args = append(args, arg)
					}
				}
				g.Expect(args).To(HaveLen(len(requiredCreateArgs())-1), "exactly the --"+missing+" flag should have been dropped")

				call, err := runCreate(t, context.Background(), nil, args...)

				g.Expect(err).To(MatchError(ContainSubstring(`required flag(s) "`+missing+`" not set`)), "--"+missing+" must be enforced as a required flag")
				g.Expect(call.called).To(BeFalse(), "create step must not run when --"+missing+" is missing")
			})
		}
	})

	t.Run("When a required flag is set to an empty value, it should fail validation without creating anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Cobra counts an explicitly empty flag as set, so the required-flag
		// check passes and only Validate catches it. This is the case that
		// proves RunE validates before doing any work.
		args := append(slices.Clone(requiredCreateArgs()), "--name=")
		call, err := runCreate(t, context.Background(), nil, args...)

		g.Expect(err).To(MatchError(ContainSubstring("name is required")), "Validate should reject an empty --name")
		g.Expect(call.called).To(BeFalse(), "create step must not run when --name is empty")
	})

	t.Run("When the create step fails, it should propagate the error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// The error has to reach the caller unwrapped so `hcp` exits non-zero
		// instead of logging the failure and reporting success.
		createErr := errors.New("failed to setup Azure credentials")
		_, err := runCreate(t, context.Background(), createErr, requiredCreateArgs()...)

		g.Expect(err).To(MatchError(createErr), "the create step error should reach the caller unwrapped")
	})

	t.Run("When the command is executed, it should pass its context to the create step", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		type ctxKey struct{}
		ctx := context.WithValue(context.Background(), ctxKey{}, "cancellable")

		call, err := runCreate(t, ctx, nil, requiredCreateArgs()...)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with the required flags")
		// A background context here would leave the Azure calls uncancellable
		// on SIGINT.
		g.Expect(call.ctx.Value(ctxKey{})).To(Equal("cancellable"), "the create step should receive the command's context, not a fresh background one")
	})

	t.Run("When an unknown flag is provided, it should fail without creating anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		args := append(slices.Clone(requiredCreateArgs()), "--managed-identities-file=/tmp/managed-identities.json")
		call, err := runCreate(t, context.Background(), nil, args...)

		g.Expect(err).To(MatchError(ContainSubstring("unknown flag: --managed-identities-file")), "an unknown flag should be rejected by the parser")
		g.Expect(call.called).To(BeFalse(), "create step must not run when an unknown flag is provided")
	})

	t.Run("When the shipped command runs, it should delegate to CreateIAMOptions.Run", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Every other case substitutes the create step, so this is the only
		// place that proves NewCreateCommand is bound to the real
		// CreateIAMOptions.Run rather than to some other function. Pointing
		// --azure-creds at a path that does not exist makes Run fail while
		// setting up credentials, before it reaches Azure.
		cmd := NewCreateCommand()
		cmd.SetArgs([]string{
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=" + filepath.Join(t.TempDir(), "azure-creds.json"),
			"--resource-group-name=test-cluster-fh7z2",
			"--oidc-issuer-url=https://oidc.example.com/test-cluster-fh7z2",
			"--output-file=" + filepath.Join(t.TempDir(), "workload-identities.json"),
		})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)

		g.Expect(cmd.ExecuteContext(context.Background())).To(MatchError(ContainSubstring("failed to setup Azure credentials")), "the shipped command should be wired to the real CreateIAMOptions.Run")
	})
}
