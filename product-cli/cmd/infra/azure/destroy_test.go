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

// destroyInfraCall records what the command handed to the destroy step.
type destroyInfraCall struct {
	called bool
	ctx    context.Context
	opts   *hypershiftazure.DestroyInfraOptions
}

// runDestroy builds the command with a recording destroy step in place of the
// real Azure call, executes it with args, and returns both what the command
// handed to that step and the error the CLI would exit with.
func runDestroy(t *testing.T, ctx context.Context, destroyErr error, args ...string) (*destroyInfraCall, error) {
	t.Helper()

	call := &destroyInfraCall{}
	cmd := newDestroyCommand(func(runCtx context.Context, opts *hypershiftazure.DestroyInfraOptions, _ logr.Logger) error {
		call.called = true
		call.ctx = runCtx
		call.opts = opts
		return destroyErr
	})
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	return call, cmd.ExecuteContext(ctx)
}

// requiredDestroyArgs is the smallest invocation that reaches Azure.
func requiredDestroyArgs() []string {
	return []string{
		"--name=test-cluster",
		"--infra-id=test-cluster-fh7z2",
		"--azure-creds=/tmp/azure-creds.json",
	}
}

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	t.Run("When the command is created, it should expose only the product flag set", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// The product CLI deliberately exposes a narrower flag set than the dev
		// CLI. Binding the dev flags here would leak unsupported flags into a
		// shipped command, so the exposed set is asserted explicitly.
		var flags []string
		NewDestroyCommand().Flags().VisitAll(func(f *pflag.Flag) {
			flags = append(flags, f.Name)
		})
		g.Expect(flags).To(ConsistOf(
			"azure-creds",
			"cloud",
			"infra-id",
			"location",
			"name",
			"preserve-resource-group",
			"resource-group-name",
		), "destroy infra azure must expose exactly the product flag set, with no dev-CLI flags leaking in")
	})

	t.Run("When all flags are provided, it should hand the parsed values to the destroy step and succeed", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runDestroy(t, context.Background(), nil,
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=/tmp/azure-creds.json",
			"--location=westus2",
			"--cloud=AzureUSGovernmentCloud",
			"--resource-group-name=test-cluster-fh7z2",
			"--preserve-resource-group=true",
		)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with all valid flags")
		g.Expect(call.called).To(BeTrue(), "destroy step must run once all flags validate")
		g.Expect(call.opts.Name).To(Equal("test-cluster"), "--name should reach the destroy step")
		g.Expect(call.opts.InfraID).To(Equal("test-cluster-fh7z2"), "--infra-id should reach the destroy step")
		g.Expect(call.opts.CredentialsFile).To(Equal("/tmp/azure-creds.json"), "--azure-creds should reach the destroy step")
		g.Expect(call.opts.Location).To(Equal("westus2"), "--location should override the default location")
		g.Expect(call.opts.Cloud).To(Equal("AzureUSGovernmentCloud"), "--cloud should override the default cloud")
		g.Expect(call.opts.ResourceGroupName).To(Equal("test-cluster-fh7z2"), "--resource-group-name should reach the destroy step")
		g.Expect(call.opts.PreserveResourceGroup).To(BeTrue(), "--preserve-resource-group=true should reach the destroy step")
	})

	t.Run("When only the required flags are provided, it should apply the documented defaults", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runDestroy(t, context.Background(), nil, requiredDestroyArgs()...)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with only the required flags")
		// The defaults have to survive all the way to the Azure call, not just
		// be present as a flag default string. Defaulting
		// --preserve-resource-group to true would silently leave the resource
		// group behind on every destroy.
		g.Expect(call.opts.Location).To(Equal(config.DefaultAzureLocation), "location should default to config.DefaultAzureLocation")
		g.Expect(call.opts.Cloud).To(Equal(config.DefaultAzureCloud), "cloud should default to config.DefaultAzureCloud")
		g.Expect(call.opts.PreserveResourceGroup).To(BeFalse(), "the resource group must be deleted unless --preserve-resource-group is set")
	})

	t.Run("When a required flag is omitted, it should fail before destroying anything", func(t *testing.T) {
		t.Parallel()

		// cmd.MarkFlagRequired reports an error for a misspelled flag name and
		// every caller discards it, so a typo there would silently drop the
		// requirement. Each flag is exercised through the command to prove the
		// requirement is actually enforced.
		for _, missing := range []string{"name", "infra-id", "azure-creds"} {
			t.Run(missing, func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				var args []string
				for _, arg := range requiredDestroyArgs() {
					if !strings.HasPrefix(arg, "--"+missing+"=") {
						args = append(args, arg)
					}
				}
				g.Expect(args).To(HaveLen(len(requiredDestroyArgs())-1), "exactly the --"+missing+" flag should have been dropped")

				call, err := runDestroy(t, context.Background(), nil, args...)

				g.Expect(err).To(MatchError(ContainSubstring(`required flag(s) "`+missing+`" not set`)), "--"+missing+" must be enforced as a required flag")
				g.Expect(call.called).To(BeFalse(), "destroy step must not run when --"+missing+" is missing")
			})
		}
	})

	t.Run("When a required flag is set to an empty value, it should fail validation without destroying anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Cobra counts an explicitly empty flag as set, so the required-flag
		// check passes and only Validate catches it. This is the case that
		// proves RunE validates before doing any work -- destroying with an
		// empty infra ID would target the wrong resource groups.
		args := append(slices.Clone(requiredDestroyArgs()), "--infra-id=")
		call, err := runDestroy(t, context.Background(), nil, args...)

		g.Expect(err).To(MatchError(ContainSubstring("infra-id is required")), "Validate should reject an empty --infra-id")
		g.Expect(call.called).To(BeFalse(), "destroy step must not run when --infra-id is empty")
	})

	t.Run("When the destroy step fails, it should propagate the error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// The error has to reach the caller unwrapped so `hcp` exits non-zero
		// instead of logging the failure and reporting success.
		destroyErr := errors.New("failed to setup Azure credentials")
		_, err := runDestroy(t, context.Background(), destroyErr, requiredDestroyArgs()...)

		g.Expect(err).To(MatchError(destroyErr), "the destroy step error should reach the caller unwrapped")
	})

	t.Run("When the command is executed, it should pass its context to the destroy step", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		type ctxKey struct{}
		ctx := context.WithValue(context.Background(), ctxKey{}, "cancellable")

		call, err := runDestroy(t, ctx, nil, requiredDestroyArgs()...)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with the required flags")
		// A background context here would leave the Azure calls uncancellable
		// on SIGINT.
		g.Expect(call.ctx.Value(ctxKey{})).To(Equal("cancellable"), "the destroy step should receive the command's context, not a fresh background one")
	})

	t.Run("When an unknown flag is provided, it should fail without destroying anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		args := append(slices.Clone(requiredDestroyArgs()), "--managed-identities-file=/tmp/managed-identities.json")
		call, err := runDestroy(t, context.Background(), nil, args...)

		g.Expect(err).To(MatchError(ContainSubstring("unknown flag: --managed-identities-file")), "an unknown flag should be rejected by the parser")
		g.Expect(call.called).To(BeFalse(), "destroy step must not run when an unknown flag is provided")
	})

	t.Run("When the shipped command runs, it should delegate to DestroyInfraOptions.Run", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Every other case substitutes the destroy step, so this is the only
		// place that proves NewDestroyCommand is bound to the real
		// DestroyInfraOptions.Run rather than to some other function. Pointing
		// --azure-creds at a path that does not exist makes Run fail while
		// setting up credentials, before it reaches Azure.
		cmd := NewDestroyCommand()
		cmd.SetArgs([]string{
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=" + filepath.Join(t.TempDir(), "azure-creds.json"),
		})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)

		g.Expect(cmd.ExecuteContext(context.Background())).To(MatchError(ContainSubstring("failed to setup Azure credentials")), "the shipped command should be wired to the real DestroyInfraOptions.Run")
	})
}
