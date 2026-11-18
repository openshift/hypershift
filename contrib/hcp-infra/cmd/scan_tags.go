package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	awspkg "github.com/openshift/hypershift/contrib/hcp-infra/pkg/aws"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/classifier"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/reporter"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// ScanFlags holds flags shared across all scan subcommands.
type ScanFlags struct {
	OlderThan       time.Duration
	Owner           string
	InfraID         string
	ExcludeInfraIDs []string
	OIDCBuckets     []string
}

// addScanFlags registers the shared scan flags on a cobra command.
func addScanFlags(cmd *cobra.Command, flags *ScanFlags) {
	cmd.Flags().DurationVar(&flags.OlderThan, "older-than", 24*time.Hour, "Skip resources newer than this duration")
	cmd.Flags().StringVar(&flags.Owner, "owner", "", "Filter by cluster-name tag prefix (e.g. 'ci-' or 'jdoe-')")
	cmd.Flags().StringVar(&flags.InfraID, "infra-id", "", "Scan a specific infra ID only")
	cmd.Flags().StringSliceVar(&flags.ExcludeInfraIDs, "exclude-infra-id", nil, "Infra IDs to skip")
	cmd.Flags().StringSliceVar(&flags.OIDCBuckets, "oidc-buckets", nil, "S3 buckets to scan for OIDC prefixes")
}

// toScanOptions converts ScanFlags to scanner.ScanOptions.
func (sf *ScanFlags) toScanOptions() scanner.ScanOptions {
	return scanner.ScanOptions{
		InfraID:         sf.InfraID,
		Owner:           sf.Owner,
		OlderThan:       sf.OlderThan,
		ExcludeInfraIDs: sf.ExcludeInfraIDs,
		OIDCBuckets:     sf.OIDCBuckets,
	}
}

// NewScanTagsCommand creates the "scan tags" subcommand for fast tag-based scanning.
func NewScanTagsCommand(globals *GlobalFlags) *cobra.Command {
	scanFlags := &ScanFlags{}

	cmd := &cobra.Command{
		Use:   "tags",
		Short: "Tag-based scan via Resource Groups Tagging API",
		Long: `Discover HyperShift infrastructure by querying the AWS Resource Groups
Tagging API for resources tagged with hypershift.openshift.io/infra-id.

This is the fastest scan mode and should be the default for most use cases.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScanTags(cmd.Context(), globals, scanFlags)
		},
	}

	addScanFlags(cmd, scanFlags)
	return cmd
}

func runScanTags(ctx context.Context, globals *GlobalFlags, scanFlags *ScanFlags) error {
	// Create AWS clients.
	clients, err := awspkg.NewClients(ctx, globals.Regions[0])
	if err != nil {
		return fmt.Errorf("initializing AWS clients: %w", err)
	}

	if !globals.Quiet {
		fmt.Fprintf(os.Stderr, "Scanning AWS account %s across %d region(s)...\n", clients.AccountID, len(globals.Regions))
	}

	// Run the tag-based scan.
	inv, err := scanner.ScanTags(ctx, clients, globals.Regions, scanFlags.toScanOptions())
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Classify groups (no management cluster cross-reference in tags mode).
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
