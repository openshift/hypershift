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

// destroyIAMCall records what the command handed to the destroy step.
type destroyIAMCall struct {
	called bool
	ctx    context.Context
	opts   *hypershiftazure.DestroyIAMOptions
}

// runDestroy builds the command with a recording destroy step in place of the
// real Azure call, executes it with args, and returns both what the command
// handed to that step and the error the CLI would exit with.
func runDestroy(t *testing.T, ctx context.Context, destroyErr error, args ...string) (*destroyIAMCall, error) {
	t.Helper()

	call := &destroyIAMCall{}
	cmd := newDestroyCommand(func(runCtx context.Context, opts *hypershiftazure.DestroyIAMOptions, _ logr.Logger) error {
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

// requiredDestroyArgs is the smallest invocation that reaches Azure. It
// includes --dns-zone-rg-name, which Validate requires even though the command
// does not mark it required.
func requiredDestroyArgs() []string {
	return []string{
		"--name=test-cluster",
		"--infra-id=test-cluster-fh7z2",
		"--workload-identities-file=/tmp/workload-identities.json",
		"--azure-creds=/tmp/azure-creds.json",
		"--resource-group-name=test-cluster-fh7z2",
		"--dns-zone-rg-name=os4-common",
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
			"dns-zone-rg-name",
			"infra-id",
			"name",
			"resource-group-name",
			"workload-identities-file",
		), "destroy iam azure must expose exactly the product flag set, with no dev-CLI flags leaking in")
	})

	t.Run("When all flags are provided, it should hand the parsed values to the destroy step and succeed", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runDestroy(t, context.Background(), nil,
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--workload-identities-file=/tmp/workload-identities.json",
			"--azure-creds=/tmp/azure-creds.json",
			"--resource-group-name=test-cluster-fh7z2",
			"--dns-zone-rg-name=os4-common",
			"--cloud=AzureUSGovernmentCloud",
		)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with all valid flags")
		g.Expect(call.called).To(BeTrue(), "destroy step must run once all flags validate")
		g.Expect(call.opts.Name).To(Equal("test-cluster"), "--name should reach the destroy step")
		g.Expect(call.opts.InfraID).To(Equal("test-cluster-fh7z2"), "--infra-id should reach the destroy step")
		g.Expect(call.opts.WorkloadIdentitiesFile).To(Equal("/tmp/workload-identities.json"), "--workload-identities-file should reach the destroy step")
		g.Expect(call.opts.CredentialsFile).To(Equal("/tmp/azure-creds.json"), "--azure-creds should reach the destroy step")
		g.Expect(call.opts.ResourceGroupName).To(Equal("test-cluster-fh7z2"), "--resource-group-name should reach the destroy step")
		g.Expect(call.opts.DNSZoneRG).To(Equal("os4-common"), "--dns-zone-rg-name should reach the destroy step")
		g.Expect(call.opts.Cloud).To(Equal("AzureUSGovernmentCloud"), "--cloud should override the default cloud")
	})

	t.Run("When only the required flags are provided, it should apply the documented defaults", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runDestroy(t, context.Background(), nil, requiredDestroyArgs()...)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with only the required flags")
		// The default has to survive all the way to the Azure call, not just be
		// present as a flag default string.
		g.Expect(call.opts.Cloud).To(Equal(config.DefaultAzureCloud), "cloud should default to config.DefaultAzureCloud")
	})

	t.Run("When a required flag is omitted, it should fail before destroying anything", func(t *testing.T) {
		t.Parallel()

		// cmd.MarkFlagRequired reports an error for a misspelled flag name and
		// every caller discards it, so a typo there would silently drop the
		// requirement. Each flag is exercised through the command to prove the
		// requirement is actually enforced.
		for _, missing := range []string{
			"name",
			"infra-id",
			"workload-identities-file",
			"azure-creds",
			"resource-group-name",
		} {
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

	t.Run("When dns-zone-rg-name is omitted, it should fail validation without destroying anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Validate requires --dns-zone-rg-name but the command does not mark it
		// required, so the failure surfaces from Validate rather than from
		// Cobra. Either way nothing must be destroyed.
		var args []string
		for _, arg := range requiredDestroyArgs() {
			if !strings.HasPrefix(arg, "--dns-zone-rg-name=") {
				args = append(args, arg)
			}
		}

		call, err := runDestroy(t, context.Background(), nil, args...)

		g.Expect(err).To(MatchError(ContainSubstring("dns-zone-rg-name is required")), "Validate should reject a missing --dns-zone-rg-name")
		g.Expect(call.called).To(BeFalse(), "destroy step must not run when --dns-zone-rg-name is missing")
	})

	t.Run("When the destroy step fails, it should propagate the error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// The error has to reach the caller unwrapped so `hcp` exits non-zero
		// instead of logging the failure and reporting success.
		destroyErr := errors.New("failed to read workload identities file")
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

	t.Run("When the shipped command runs, it should delegate to DestroyIAMOptions.Run", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Every other case substitutes the destroy step, so this is the only
		// place that proves NewDestroyCommand is bound to the real
		// DestroyIAMOptions.Run rather than to some other function. Pointing
		// --workload-identities-file at a path that does not exist makes Run
		// fail on its first read, before it reaches Azure.
		cmd := NewDestroyCommand()
		cmd.SetArgs([]string{
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--workload-identities-file=" + filepath.Join(t.TempDir(), "workload-identities.json"),
			"--azure-creds=" + filepath.Join(t.TempDir(), "azure-creds.json"),
			"--resource-group-name=test-cluster-fh7z2",
			"--dns-zone-rg-name=os4-common",
		})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)

		g.Expect(cmd.ExecuteContext(context.Background())).To(MatchError(ContainSubstring("failed to read workload identities file")), "the shipped command should be wired to the real DestroyIAMOptions.Run")
	})
}
