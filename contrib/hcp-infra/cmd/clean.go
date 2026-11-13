package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	awspkg "github.com/openshift/hypershift/contrib/hcp-infra/pkg/aws"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/classifier"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/reporter"
	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// CleanFlags holds flags specific to the clean command.
type CleanFlags struct {
	InfraID         string
	All             bool
	FromFile        string
	OlderThan       time.Duration
	Owner           string
	ExcludeInfraIDs []string
	OIDCBuckets     []string
	Yes             bool
}

// NewCleanCommand creates the clean subcommand for removing orphaned resources.
func NewCleanCommand(globals *GlobalFlags) *cobra.Command {
	flags := &CleanFlags{}

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Clean orphaned HyperShift infrastructure resources",
		Long: `Remove AWS resources belonging to orphaned HyperShift clusters.

Requires one of:
  --infra-id <ID>   Clean a specific infra ID
  --all             Clean all discovered orphans
  --from-file <f>   Clean infra IDs listed in a JSON scan output file

Safety: displays the AWS account ID and deletion plan before proceeding.
Use --dry-run to preview without making changes.
Use --yes to skip the interactive confirmation prompt.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClean(cmd.Context(), globals, flags)
		},
	}

	cmd.Flags().StringVar(&flags.InfraID, "infra-id", "", "Clean a specific infra ID")
	cmd.Flags().BoolVar(&flags.All, "all", false, "Clean all discovered orphans")
	cmd.Flags().StringVar(&flags.FromFile, "from-file", "", "JSON scan output file with infra IDs to clean")
	cmd.Flags().DurationVar(&flags.OlderThan, "older-than", 24*time.Hour, "Skip resources newer than this duration")
	cmd.Flags().StringVar(&flags.Owner, "owner", "", "Filter by cluster-name tag prefix")
	cmd.Flags().StringSliceVar(&flags.ExcludeInfraIDs, "exclude-infra-id", nil, "Infra IDs to skip")
	cmd.Flags().StringSliceVar(&flags.OIDCBuckets, "oidc-buckets", nil, "S3 buckets to scan for OIDC prefixes")
	cmd.Flags().BoolVarP(&flags.Yes, "yes", "y", false, "Skip interactive confirmation")

	return cmd
}

func runClean(ctx context.Context, globals *GlobalFlags, flags *CleanFlags) error {
	// Validate: must have one of --infra-id, --all, or --from-file.
	if flags.InfraID == "" && !flags.All && flags.FromFile == "" {
		return fmt.Errorf("one of --infra-id, --all, or --from-file is required")
	}

	// Count how many target selectors are set.
	selectorCount := 0
	if flags.InfraID != "" {
		selectorCount++
	}
	if flags.All {
		selectorCount++
	}
	if flags.FromFile != "" {
		selectorCount++
	}
	if selectorCount > 1 {
		return fmt.Errorf("only one of --infra-id, --all, or --from-file may be specified")
	}

	// If --from-file, load the inventory from the file.
	if flags.FromFile != "" {
		return runCleanFromFile(ctx, globals, flags)
	}

	// Create AWS clients.
	clients, err := awspkg.NewClients(ctx, globals.Regions[0])
	if err != nil {
		return fmt.Errorf("initializing AWS clients: %w", err)
	}

	// Print account ID prominently.
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  ⚠  AWS Account: %s\n", clients.AccountID)
	fmt.Fprintf(os.Stderr, "\n")

	// Build scan options.
	scanOpts := scanner.ScanOptions{
		OlderThan:       flags.OlderThan,
		Owner:           flags.Owner,
		ExcludeInfraIDs: flags.ExcludeInfraIDs,
		OIDCBuckets:     flags.OIDCBuckets,
	}
	if flags.InfraID != "" {
		scanOpts.InfraID = flags.InfraID
	}

	if !globals.Quiet {
		fmt.Fprintf(os.Stderr, "Scanning for resources to clean...\n")
	}

	// Scan.
	inv, err := scanner.ScanTags(ctx, clients, globals.Regions, scanOpts)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Classify.
	classifier.Classify(inv, nil)

	// Determine which groups to clean.
	var toClean []*scanner.InfraGroup
	if flags.InfraID != "" {
		// Clean a specific infra ID regardless of classification.
		if g, ok := inv.Groups[flags.InfraID]; ok {
			toClean = append(toClean, g)
		} else {
			return fmt.Errorf("infra ID %q not found in scan results", flags.InfraID)
		}
	} else {
		// --all: clean all orphaned groups.
		toClean = inv.OrphanedGroups()
	}

	if len(toClean) == 0 {
		fmt.Fprintf(os.Stderr, "No orphaned resources found. Nothing to clean.\n")
		return nil
	}

	// Display what will be cleaned.
	fmt.Fprintf(os.Stderr, "\n")
	rep := reporter.New("text")
	if err := rep.Report(os.Stderr, buildCleanPreview(inv, toClean)); err != nil {
		return fmt.Errorf("writing preview: %w", err)
	}

	totalResources := 0
	for _, g := range toClean {
		totalResources += len(g.Resources)
	}

	fmt.Fprintf(os.Stderr, "\n  Will delete %d resources across %d infra ID(s).\n\n",
		totalResources, len(toClean))

	// Show per-resource deletion plan (both dry-run and execute).
	for _, g := range toClean {
		fmt.Fprintf(os.Stderr, "  %s (cluster: %s)\n", g.InfraID, g.ClusterName)
		for i, r := range g.Resources {
			prefix := "  [dry-run]"
			if !globals.DryRun {
				prefix = fmt.Sprintf("  [%d/%d]  ", i+1, totalResources)
			}
			if r.Name != "" {
				fmt.Fprintf(os.Stderr, "%s Delete %-20s %s (%s)\n", prefix, r.Type, r.ID, r.Name)
			} else {
				fmt.Fprintf(os.Stderr, "%s Delete %-20s %s\n", prefix, r.Type, r.ID)
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	// Dry-run: stop here.
	if globals.DryRun {
		fmt.Fprintf(os.Stderr, "DRY RUN: No changes made.\nRun without --dry-run to delete.\n")
		return nil
	}

	// Confirmation prompt.
	if !flags.Yes && isTTY() {
		fmt.Fprintf(os.Stderr, "  Type 'yes' to proceed: ")
		reader := bufio.NewReader(os.Stdin)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		answer = strings.TrimSpace(answer)
		if answer != "yes" {
			fmt.Fprintf(os.Stderr, "Aborted.\n")
			return nil
		}
	}

	// Execute deletion.
	fmt.Fprintf(os.Stderr, "Cleaning...\n")
	hasErrors := false
	for _, g := range toClean {
		if err := cleanInfraGroup(ctx, clients, g, globals); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR cleaning %s: %v\n", g.InfraID, err)
			hasErrors = true
		}
	}

	if hasErrors {
		fmt.Fprintf(os.Stderr, "\nCompleted with errors.\n")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Done. Cleaned %d infra ID(s).\n", len(toClean))
	return nil
}

// runCleanFromFile loads a JSON scan output and cleans the orphaned groups.
func runCleanFromFile(ctx context.Context, globals *GlobalFlags, flags *CleanFlags) error {
	data, err := os.ReadFile(flags.FromFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w", flags.FromFile, err)
	}

	// Parse the JSON inventory.
	var jsonInv jsonScanFile
	if err := json.Unmarshal(data, &jsonInv); err != nil {
		return fmt.Errorf("parsing %s: %w", flags.FromFile, err)
	}

	// Create AWS clients.
	clients, err := awspkg.NewClients(ctx, globals.Regions[0])
	if err != nil {
		return fmt.Errorf("initializing AWS clients: %w", err)
	}

	// Print account ID.
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  ⚠  AWS Account: %s\n", clients.AccountID)
	fmt.Fprintf(os.Stderr, "  Source file:     %s\n", flags.FromFile)
	fmt.Fprintf(os.Stderr, "\n")

	// Verify account matches.
	if jsonInv.AccountID != "" && jsonInv.AccountID != clients.AccountID {
		return fmt.Errorf("account mismatch: file has %s but current credentials are for %s",
			jsonInv.AccountID, clients.AccountID)
	}

	// Extract orphaned infra IDs from the file.
	var infraIDs []string
	for _, g := range jsonInv.Groups {
		cat := strings.ToUpper(g.Category)
		if strings.Contains(cat, "ORPHAN") {
			infraIDs = append(infraIDs, g.InfraID)
		}
	}

	if len(infraIDs) == 0 {
		fmt.Fprintf(os.Stderr, "No orphaned infra IDs found in %s. Nothing to clean.\n", flags.FromFile)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found %d orphaned infra ID(s) in %s:\n", len(infraIDs), flags.FromFile)
	for _, id := range infraIDs {
		fmt.Fprintf(os.Stderr, "  - %s\n", id)
	}
	fmt.Fprintln(os.Stderr)

	if globals.DryRun {
		fmt.Fprintf(os.Stderr, "DRY RUN: Would re-scan and clean these infra IDs.\n")
		return nil
	}

	// Confirmation.
	if !flags.Yes && isTTY() {
		fmt.Fprintf(os.Stderr, "  Type 'yes' to proceed: ")
		reader := bufio.NewReader(os.Stdin)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if strings.TrimSpace(answer) != "yes" {
			fmt.Fprintf(os.Stderr, "Aborted.\n")
			return nil
		}
	}

	// Re-scan each infra ID and clean.
	hasErrors := false
	for _, infraID := range infraIDs {
		fmt.Fprintf(os.Stderr, "Scanning %s...\n", infraID)

		scanOpts := scanner.ScanOptions{
			InfraID:   infraID,
			OlderThan: flags.OlderThan,
		}
		inv, err := scanner.ScanTags(ctx, clients, globals.Regions, scanOpts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR scanning %s: %v\n", infraID, err)
			hasErrors = true
			continue
		}

		for _, g := range inv.Groups {
			if err := cleanInfraGroup(ctx, clients, g, globals); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR cleaning %s: %v\n", g.InfraID, err)
				hasErrors = true
			}
		}
	}

	if hasErrors {
		fmt.Fprintf(os.Stderr, "\nCompleted with errors.\n")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Done. Cleaned %d infra ID(s).\n", len(infraIDs))
	return nil
}

// cleanInfraGroup deletes all resources in an infra group.
// TODO: Implement actual deletion via cleaner package. For now, this logs
// what it would do.
func cleanInfraGroup(_ context.Context, _ *awspkg.Clients, g *scanner.InfraGroup, globals *GlobalFlags) error {
	for _, r := range g.Resources {
		if globals.Verbose {
			fmt.Fprintf(os.Stderr, "  [DELETE] %s %s (%s) in %s\n", r.Type, r.ID, r.Name, r.Region)
		} else {
			fmt.Fprintf(os.Stderr, "  [DELETE] %s %s in %s\n", r.Type, r.ID, r.Region)
		}
	}
	// TODO: Call cleaner package to actually delete resources.
	// The cleaner should handle deletion ordering (e.g., instances before VPCs,
	// NAT gateways before subnets, etc.) and retry on dependency errors.
	fmt.Fprintf(os.Stderr, "  → %s: %d resources (deletion not yet implemented)\n", g.InfraID, len(g.Resources))
	return nil
}

// buildCleanPreview creates a preview inventory containing only the groups to be cleaned.
func buildCleanPreview(origInv *scanner.Inventory, groups []*scanner.InfraGroup) *scanner.Inventory {
	preview := scanner.NewInventory(origInv.AccountID, origInv.Regions, origInv.ScanMode)
	preview.ScannedAt = origInv.ScannedAt
	for _, g := range groups {
		preview.Groups[g.InfraID] = g
	}
	return preview
}

// isTTY returns true if stdin is a terminal (not a pipe/redirect).
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// jsonScanFile is a minimal struct to parse the JSON output from "scan -o json".
type jsonScanFile struct {
	AccountID string           `json:"account_id"`
	Groups    []jsonScanGroup  `json:"groups"`
}

type jsonScanGroup struct {
	InfraID  string `json:"infra_id"`
	Category string `json:"category"`
}
