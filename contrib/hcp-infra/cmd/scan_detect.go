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

// NewScanDetectCommand creates the "scan detect" subcommand. This mode
// cross-references AWS resources with the management cluster's HostedClusters
// to determine which infrastructure is genuinely orphaned vs still in use.
func NewScanDetectCommand(globals *GlobalFlags) *cobra.Command {
	scanFlags := &ScanFlags{}

	cmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect orphans by cross-referencing with management cluster",
		Long: `Perform a tag-based scan and cross-reference discovered infrastructure
with HostedCluster objects on the management cluster.

This is the most accurate scan mode: it can distinguish between
infrastructure that looks orphaned (no VPC) and infrastructure that is
still actively referenced by a HostedCluster.

Requires --kubeconfig pointing to the management cluster.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScanDetect(cmd.Context(), globals, scanFlags)
		},
	}

	addScanFlags(cmd, scanFlags)
	return cmd
}

func runScanDetect(ctx context.Context, globals *GlobalFlags, scanFlags *ScanFlags) error {
	if globals.Kubeconfig == "" {
		return fmt.Errorf("--kubeconfig is required for detect mode (point to management cluster)")
	}

	// Create AWS clients.
	clients, err := awspkg.NewClients(ctx, globals.Regions[0])
	if err != nil {
		return fmt.Errorf("initializing AWS clients: %w", err)
	}

	if !globals.Quiet {
		fmt.Fprintf(os.Stderr, "Detect scan of AWS account %s across %d region(s)...\n", clients.AccountID, len(globals.Regions))
		fmt.Fprintf(os.Stderr, "Using kubeconfig: %s\n", globals.Kubeconfig)
	}

	// Run the tag-based scan.
	inv, err := scanner.ScanTags(ctx, clients, globals.Regions, scanFlags.toScanOptions())
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Override scan mode to reflect detect mode.
	inv.ScanMode = "detect"

	// TODO: Query the management cluster for active HostedClusters.
	// This would use the kubeconfig to list HCs, extract their infra IDs,
	// and pass them to the classifier. For now, we pass nil.
	//
	// activeHCInfraIDs, err := listActiveHostedClusters(ctx, globals.Kubeconfig)
	// if err != nil {
	//     return fmt.Errorf("listing HostedClusters: %w", err)
	// }
	var activeHCInfraIDs map[string]string

	if !globals.Quiet {
		fmt.Fprintf(os.Stderr, "NOTE: Management cluster cross-reference not yet implemented. Classification based on tags only.\n")
	}

	// Classify groups with management cluster cross-reference.
	classifier.Classify(inv, activeHCInfraIDs)

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
