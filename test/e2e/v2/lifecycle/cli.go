//go:build e2ev2

package lifecycle

import (
	"fmt"
	"os"
	"sort"
	"strings"

	devcreate "github.com/openshift/hypershift/cmd/create"
	devdestroy "github.com/openshift/hypershift/cmd/destroy"
	productcreate "github.com/openshift/hypershift/product-cli/cmd/create"
	productdestroy "github.com/openshift/hypershift/product-cli/cmd/destroy"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CLIKind identifies which HyperShift CLI a binary implements. The two CLIs
// share subcommand paths for cluster create/destroy but bind different flag
// sets, so args must be built for a specific kind.
type CLIKind string

const (
	// CLIKindHypershift is the developer CLI built from ./cmd. It exposes
	// every subcommand the framework uses, including install, dump, fix and
	// consolelogs, and binds developer-only flags such as --infra-json.
	CLIKindHypershift CLIKind = "hypershift"

	// CLIKindHCP is the product CLI built from ./product-cli. It only exposes
	// create and destroy, and binds the customer-facing subset of the
	// developer CLI's flags.
	CLIKindHCP CLIKind = "hcp"
)

const (
	// HypershiftBinaryEnvVar names the developer CLI binary.
	HypershiftBinaryEnvVar = "HYPERSHIFT_BINARY"

	// HCPBinaryEnvVar names the product CLI binary. Setting it switches
	// HostedCluster create/destroy onto the product CLI.
	HCPBinaryEnvVar = "HCP_BINARY"

	defaultHypershiftBinary = "hypershift"
)

// CLI pairs a CLI binary with the kind of CLI it implements.
type CLI struct {
	Kind   CLIKind
	Binary string
}

// IsHCP reports whether this CLI is the product CLI.
func (c CLI) IsHCP() bool { return c.Kind == CLIKindHCP }

// String renders the CLI for log messages, e.g. `/hypershift/bin/hcp (hcp)`.
func (c CLI) String() string { return fmt.Sprintf("%s (%s)", c.Binary, c.Kind) }

// DeveloperCLI returns the CLI used for operations the product CLI does not
// implement (install, dump, fix, consolelogs). It reads HYPERSHIFT_BINARY and
// defaults to "hypershift" on PATH.
func DeveloperCLI() CLI {
	return CLI{
		Kind:   CLIKindHypershift,
		Binary: envOrDefault(HypershiftBinaryEnvVar, defaultHypershiftBinary),
	}
}

// LifecycleCLI returns the CLI used to create and destroy HostedClusters.
// HCP_BINARY selects the product CLI and takes precedence; otherwise the
// developer CLI from HYPERSHIFT_BINARY is used.
func LifecycleCLI() CLI {
	if binary := os.Getenv(HCPBinaryEnvVar); binary != "" {
		return CLI{Kind: CLIKindHCP, Binary: binary}
	}
	return DeveloperCLI()
}

// ValidateArgs reports flags in args that the CLI does not accept. The leading
// non-flag elements of args select a subcommand (e.g. "create cluster azure")
// and everything after is checked against that subcommand's local and
// inherited flags. It returns an error when the subcommand does not exist or
// when any flag is unknown, so an unsupported flag fails before a cluster is
// half provisioned rather than inside the CLI.
//
// Flags are resolved from the same cobra command trees the CLIs build, so the
// check cannot drift from the binaries it validates.
func ValidateArgs(cli CLI, args []string) error {
	cmd, remaining, err := resolveSubcommand(rootCommand(cli.Kind), args)
	if err != nil {
		return err
	}

	known := knownFlags(cmd)
	var unknown []string
	for _, name := range flagNames(remaining) {
		if !known[name] {
			unknown = append(unknown, "--"+name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%q does not accept %s (the %s CLI binds a different flag set than the %s CLI)",
		cmd.CommandPath(), strings.Join(unknown, ", "), cli.Kind, otherKind(cli.Kind))
}

func otherKind(kind CLIKind) CLIKind {
	if kind == CLIKindHCP {
		return CLIKindHypershift
	}
	return CLIKindHCP
}

// rootCommand builds the subcommand tree for a CLI kind. Only the create and
// destroy trees are built because those are the only commands the lifecycle
// binaries invoke through a configurable CLI.
func rootCommand(kind CLIKind) *cobra.Command {
	root := &cobra.Command{Use: string(kind)}
	if kind == CLIKindHCP {
		root.AddCommand(productcreate.NewCommand(), productdestroy.NewCommand())
	} else {
		root.AddCommand(devcreate.NewCommand(), devdestroy.NewCommand())
	}
	return root
}

// resolveSubcommand walks args from cmd until the first flag, returning the
// selected subcommand and the unconsumed args.
func resolveSubcommand(cmd *cobra.Command, args []string) (*cobra.Command, []string, error) {
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return cmd, args[i:], nil
		}
		child := findSubcommand(cmd, arg)
		if child == nil {
			return nil, nil, fmt.Errorf("%q has no subcommand %q", cmd.CommandPath(), arg)
		}
		cmd = child
	}
	return cmd, nil, nil
}

func findSubcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

// knownFlags returns the long names of every flag cmd accepts, including the
// persistent flags it inherits from its parents.
func knownFlags(cmd *cobra.Command) map[string]bool {
	names := map[string]bool{}
	collect := func(flags *pflag.FlagSet) {
		flags.VisitAll(func(f *pflag.Flag) { names[f.Name] = true })
	}
	collect(cmd.LocalFlags())
	collect(cmd.InheritedFlags())
	return names
}

// flagNames extracts the long flag names from args. Anything not prefixed
// with "--" is treated as a value and ignored, which is sound because the
// lifecycle binaries only ever emit long flags in `--flag=value` form.
func flagNames(args []string) []string {
	var names []string
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		name, _, _ = strings.Cut(name, "=")
		names = append(names, name)
	}
	return names
}
