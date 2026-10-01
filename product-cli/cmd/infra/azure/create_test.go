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

// createInfraCall records what the command handed to the create step.
type createInfraCall struct {
	called bool
	ctx    context.Context
	opts   *hypershiftazure.CreateInfraOptions
}

// runCreate builds the command with a recording create step in place of the
// real Azure call, executes it with args, and returns both what the command
// handed to that step and the error the CLI would exit with.
func runCreate(t *testing.T, ctx context.Context, createErr error, args ...string) (*createInfraCall, error) {
	t.Helper()

	call := &createInfraCall{}
	cmd := newCreateCommand(func(runCtx context.Context, opts *hypershiftazure.CreateInfraOptions, _ logr.Logger) (*hypershiftazure.CreateInfraOutput, error) {
		call.called = true
		call.ctx = runCtx
		call.opts = opts
		if createErr != nil {
			return nil, createErr
		}
		return &hypershiftazure.CreateInfraOutput{InfraID: opts.InfraID}, nil
	})
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	return call, cmd.ExecuteContext(ctx)
}

// requiredCreateArgs is the smallest invocation that reaches Azure. It includes
// --base-domain, which Validate requires even though the command does not mark
// it required.
func requiredCreateArgs() []string {
	return []string{
		"--name=test-cluster",
		"--infra-id=test-cluster-fh7z2",
		"--azure-creds=/tmp/azure-creds.json",
		"--base-domain=example.com",
	}
}

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	t.Run("When the command is created, it should expose only the product flag set", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// The product CLI deliberately exposes a narrower flag set than the dev
		// CLI, which also carries the ARO HCP managed-identity flags. Binding
		// the dev flags here would leak unsupported flags into a shipped
		// command, so the exposed set is asserted explicitly.
		var flags []string
		NewCreateCommand().Flags().VisitAll(func(f *pflag.Flag) {
			flags = append(flags, f.Name)
		})
		g.Expect(flags).To(ConsistOf(
			"assign-custom-hcp-roles",
			"assign-identity-roles",
			"azure-creds",
			"base-domain",
			"cloud",
			"disable-cluster-capabilities",
			"dns-zone-rg-name",
			"infra-id",
			"location",
			"name",
			"network-security-group-id",
			"output-file",
			"resource-group-name",
			"resource-group-tags",
			"subnet-id",
			"vnet-id",
			"workload-identities-file",
		), "create infra azure must expose exactly the product flag set, with no dev-CLI or ARO HCP managed-identity flags leaking in")
	})

	t.Run("When all flags are provided, it should hand the parsed values to the create step and succeed", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		call, err := runCreate(t, context.Background(), nil,
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=/tmp/azure-creds.json",
			"--base-domain=example.com",
			"--location=westus2",
			"--cloud=AzureUSGovernmentCloud",
			"--resource-group-name=test-cluster-fh7z2",
			"--resource-group-tags=owner=hypershift,env=ci",
			"--vnet-id=/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
			"--subnet-id=/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/default",
			"--network-security-group-id=/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
			"--workload-identities-file=/tmp/workload-identities.json",
			"--assign-identity-roles=true",
			"--assign-custom-hcp-roles=true",
			"--dns-zone-rg-name=os4-common",
			"--disable-cluster-capabilities=ImageRegistry",
			"--output-file=/tmp/infra.json",
		)

		g.Expect(err).ToNot(HaveOccurred(), "command should succeed with all valid flags")
		g.Expect(call.called).To(BeTrue(), "create step must run once all flags validate")
		g.Expect(call.opts.Name).To(Equal("test-cluster"), "--name should reach the create step")
		g.Expect(call.opts.InfraID).To(Equal("test-cluster-fh7z2"), "--infra-id should reach the create step")
		g.Expect(call.opts.CredentialsFile).To(Equal("/tmp/azure-creds.json"), "--azure-creds should reach the create step")
		g.Expect(call.opts.BaseDomain).To(Equal("example.com"), "--base-domain should reach the create step")
		g.Expect(call.opts.Location).To(Equal("westus2"), "--location should override the default location")
		g.Expect(call.opts.Cloud).To(Equal("AzureUSGovernmentCloud"), "--cloud should override the default cloud")
		g.Expect(call.opts.ResourceGroupName).To(Equal("test-cluster-fh7z2"), "--resource-group-name should reach the create step")
		g.Expect(call.opts.ResourceGroupTags).To(Equal(map[string]string{"owner": "hypershift", "env": "ci"}), "--resource-group-tags should parse into a key/value map")
		g.Expect(call.opts.VnetID).To(ContainSubstring("/virtualNetworks/vnet"), "--vnet-id should reach the create step")
		g.Expect(call.opts.SubnetID).To(ContainSubstring("/subnets/default"), "--subnet-id should reach the create step")
		g.Expect(call.opts.NetworkSecurityGroupID).To(ContainSubstring("/networkSecurityGroups/nsg"), "--network-security-group-id should reach the create step")
		g.Expect(call.opts.WorkloadIdentitiesFile).To(Equal("/tmp/workload-identities.json"), "--workload-identities-file should reach the create step")
		g.Expect(call.opts.AssignServicePrincipalRoles).To(BeTrue(), "--assign-identity-roles=true should reach the create step")
		g.Expect(call.opts.AssignCustomHCPRoles).To(BeTrue(), "--assign-custom-hcp-roles=true should reach the create step")
		g.Expect(call.opts.DNSZoneRG).To(Equal("os4-common"), "--dns-zone-rg-name should reach the create step")
		g.Expect(call.opts.DisableClusterCapabilities).To(Equal([]string{"ImageRegistry"}), "--disable-cluster-capabilities should parse into a string slice")
		g.Expect(call.opts.OutputFile).To(Equal("/tmp/infra.json"), "--output-file should reach the create step")
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
		g.Expect(call.opts.AssignServicePrincipalRoles).To(BeFalse(), "identity role assignment must stay off unless --assign-identity-roles is set")
		g.Expect(call.opts.AssignCustomHCPRoles).To(BeFalse(), "custom HCP role assignment must stay off unless --assign-custom-hcp-roles is set")
	})

	t.Run("When a required flag is omitted, it should fail before creating anything", func(t *testing.T) {
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

	t.Run("When base-domain is omitted, it should fail validation without creating anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Validate requires --base-domain but the command does not mark it
		// required, so the failure surfaces from Validate rather than from
		// Cobra. Either way nothing must be created.
		call, err := runCreate(t, context.Background(), nil,
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=/tmp/azure-creds.json",
		)

		g.Expect(err).To(MatchError(ContainSubstring("--base-domain is required")), "Validate should reject a missing --base-domain")
		g.Expect(call.called).To(BeFalse(), "create step must not run when --base-domain is missing")
	})

	t.Run("When role assignment is requested without a DNS zone resource group, it should fail validation without creating anything", func(t *testing.T) {
		t.Parallel()

		// Role assignments are scoped to the DNS zone resource group, so
		// requesting them without one would create infrastructure that is then
		// left half configured.
		for _, roleFlag := range []string{"--assign-identity-roles=true", "--assign-custom-hcp-roles=true"} {
			t.Run(roleFlag, func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				args := append(slices.Clone(requiredCreateArgs()), roleFlag)
				call, err := runCreate(t, context.Background(), nil, args...)

				g.Expect(err).To(MatchError(ContainSubstring("--dns-zone-rg-name is required")), "Validate should reject "+roleFlag+" without --dns-zone-rg-name")
				g.Expect(call.called).To(BeFalse(), "create step must not run when "+roleFlag+" is set without --dns-zone-rg-name")
			})
		}
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

	t.Run("When an ARO HCP identity flag is provided, it should fail without creating anything", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// --managed-identities-file belongs to the ARO HCP deployment model and
		// is intentionally not part of the self-managed product CLI.
		args := append(slices.Clone(requiredCreateArgs()), "--managed-identities-file=/tmp/managed-identities.json")
		call, err := runCreate(t, context.Background(), nil, args...)

		g.Expect(err).To(MatchError(ContainSubstring("unknown flag: --managed-identities-file")), "the ARO HCP --managed-identities-file flag must not be exposed by the product CLI")
		g.Expect(call.called).To(BeFalse(), "create step must not run when an ARO HCP identity flag is provided")
	})

	t.Run("When the shipped command runs, it should delegate to CreateInfraOptions.Run", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Every other case substitutes the create step, so this is the only
		// place that proves NewCreateCommand is bound to the real
		// CreateInfraOptions.Run rather than to some other function. Run needs
		// an identity config to get past its deployment-model check, then fails
		// setting up credentials from a path that does not exist, before it
		// reaches Azure.
		cmd := NewCreateCommand()
		cmd.SetArgs([]string{
			"--name=test-cluster",
			"--infra-id=test-cluster-fh7z2",
			"--azure-creds=" + filepath.Join(t.TempDir(), "azure-creds.json"),
			"--base-domain=example.com",
			"--workload-identities-file=" + filepath.Join(t.TempDir(), "workload-identities.json"),
		})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)

		g.Expect(cmd.ExecuteContext(context.Background())).To(MatchError(ContainSubstring("failed to setup Azure credentials")), "the shipped command should be wired to the real CreateInfraOptions.Run")
	})
}
