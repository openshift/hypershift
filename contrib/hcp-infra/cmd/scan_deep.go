package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	awspkg "github.com/openshift/hypershift/contrib/hcp-infra/pkg/aws"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/classifier"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/reporter"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// NewScanDeepCommand creates the "scan deep" subcommand.
func NewScanDeepCommand(globals *GlobalFlags) *cobra.Command {
	scanFlags := &ScanFlags{}

	cmd := &cobra.Command{
		Use:   "deep",
		Short: "Deep scan with supplemental API calls for untagged resources",
		Long: `Perform a tag-based scan plus supplemental API calls to find resources
that may not be tagged with the HyperShift infra-id tag.

This mode is slower but more thorough. It discovers orphaned resources
like detached EBS volumes, unused security groups, and NAT gateways
that lost their tags during partial cleanup.

NOTE: Deep scan is not yet fully implemented. Currently falls back to
the tag-based scan with a warning.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScanDeep(cmd.Context(), globals, scanFlags)
		},
	}

	addScanFlags(cmd, scanFlags)
	return cmd
}

func runScanDeep(ctx context.Context, globals *GlobalFlags, scanFlags *ScanFlags) error {
	// Create AWS clients.
	clients, err := awspkg.NewClients(ctx, globals.Regions[0])
	if err != nil {
		return fmt.Errorf("initializing AWS clients: %w", err)
	}

	if !globals.Quiet {
		fmt.Fprintf(os.Stderr, "Deep scan of AWS account %s across %d region(s)...\n", clients.AccountID, len(globals.Regions))
		fmt.Fprintf(os.Stderr, "NOTE: Deep scan is not yet fully implemented, falling back to tag-based scan.\n")
	}

	// TODO: Implement deep scan. For now, fall back to tag-based scan.
	inv, err := scanner.ScanTags(ctx, clients, globals.Regions, scanFlags.toScanOptions())
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Override scan mode in the inventory to reflect intent.
	inv.ScanMode = "deep"

	// Classify groups (no management cluster cross-reference).
	classifier.Classify(inv, nil)

	// Output via reporter.
	rep := reporter.New(globals.Output)
	if err := rep.Report(os.Stdout, inv); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}

	// Exit code 1 if orphans found.
	if len(inv.OrphanedGroups()) > 0 {
		os.Exit(1)
	}

	return nil
}
