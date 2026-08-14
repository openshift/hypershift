package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/spf13/cobra"
)

func main() {
	cfg := DefaultConfig()

	rootCmd := &cobra.Command{
		Use:   "cleanleaked",
		Short: "Detect and clean leaked AWS resources from HyperShift CI",
		Long: `cleanleaked scans AWS for orphaned VPCs and associated resources left behind
by failed HyperShift CI test runs, classifies them by safety, and optionally
deletes them.

Each VPC is classified through a series of gates:
  PROTECTED  - Management cluster or developer resource (never touched)
  TOO_YOUNG  - Created less than --min-age ago (skipped)
  ACTIVE     - OIDC S3 doc or EC2 instances still exist (live cluster)
  UNCERTAIN  - No CI pattern match; may belong to a developer
  LEAKED     - No OIDC, no instances, matches CI pattern (safe to delete)

Examples:
  # Scan and report all VPCs
  cleanleaked scan

  # Scan with JSON output
  cleanleaked scan --output json

  # Preview what would be deleted (no actual deletion)
  cleanleaked delete --dry-run

  # Delete first 5 leaked infra sets, prompting for each one
  cleanleaked delete --limit 5 --interactive

  # Delete all leaked infra sets without prompts (CI-safe)
  cleanleaked delete --confirm

  # Delete with a 48h age threshold
  cleanleaked delete --min-age 48h --confirm

  # Scan a single VPC by ID
  cleanleaked scan --target vpc-066231b8dc5a4400f

  # Scan a single infra set by infraID
  cleanleaked scan --target node-pool-78vcg

  # Delete a specific infra set interactively
  cleanleaked delete --target 00ab3695c5f73d4354b9 --interactive`,
	}

	scanCmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan for leaked VPCs and associated resources",
		Long: `Scan all VPCs in the region and classify each by safety verdict.

The report includes sub-resource inventory, Route53 zones, OIDC providers,
IAM roles, Prow job correlation, and creation timestamps.

Examples:
  cleanleaked scan
  cleanleaked scan --output json
  cleanleaked scan --min-age 48h
  cleanleaked scan --target vpc-066231b8dc5a4400f
  cleanleaked scan --target node-pool-78vcg`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(cmd.Context(), cfg)
		},
	}

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete leaked VPCs and associated resources",
		Long: `Delete leaked VPCs and all their dependent resources in cascade order:
  ELBs → VPCE Services → VPCE → NAT → EIPs → IGW → RTB → ENI → Subnets → SG → VPC
  Then: Route53 zones, OIDC providers, IAM roles.

Three modes:
  delete                Prompt once at the start, then delete all
  delete --confirm      No prompts, delete all immediately (CI-safe)
  delete --interactive  Show full inventory per infra set, prompt for each

Safety: resources with do-not-delete=true, protected VPC names, protected
usernames, running instances, or unexpired expirationDate are NEVER deleted.

Examples:
  cleanleaked delete --dry-run
  cleanleaked delete --limit 10 --interactive
  cleanleaked delete --confirm --min-age 48h
  cleanleaked delete --target node-pool-78vcg --interactive
  cleanleaked delete --target vpc-0abc123 --confirm`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Delete = true
			return runDelete(cmd.Context(), cfg)
		},
	}

	scanCmd.Flags().StringVar(&cfg.Region, "region", cfg.Region, "AWS region")
	scanCmd.Flags().DurationVar(&cfg.MinAge, "min-age", cfg.MinAge, "Minimum VPC age to consider for deletion")
	scanCmd.Flags().StringVar(&cfg.Target, "target", cfg.Target, "Scan a single infraID or VPC ID instead of all VPCs")
	scanCmd.Flags().StringVar(&cfg.OutputFormat, "output", cfg.OutputFormat, "Output format: table or json")
	scanCmd.Flags().StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "Directory for report files (empty to disable)")

	deleteCmd.Flags().StringVar(&cfg.Region, "region", cfg.Region, "AWS region")
	deleteCmd.Flags().DurationVar(&cfg.MinAge, "min-age", cfg.MinAge, "Minimum VPC age to consider for deletion")
	deleteCmd.Flags().StringVar(&cfg.Target, "target", cfg.Target, "Delete a single infraID or VPC ID instead of all leaked")
	deleteCmd.Flags().BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "Show what would be deleted without deleting")
	deleteCmd.Flags().BoolVar(&cfg.Confirm, "confirm", cfg.Confirm, "No prompts, delete all leaked resources immediately")
	deleteCmd.Flags().BoolVarP(&cfg.Interactive, "interactive", "i", cfg.Interactive, "Prompt before each individual resource deletion")
	deleteCmd.Flags().IntVar(&cfg.Limit, "limit", cfg.Limit, "Max number of infra sets to delete (0 = no limit)")
	deleteCmd.Flags().StringVar(&cfg.OutputFormat, "output", cfg.OutputFormat, "Output format: table or json")
	deleteCmd.Flags().StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "Directory for report files (empty to disable)")

	rootCmd.AddCommand(scanCmd, deleteCmd)

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

func newScanner(ctx context.Context, cfg Config) (*Scanner, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &Scanner{
		EC2:     ec2.NewFromConfig(awsCfg),
		ELBv2:   elbv2.NewFromConfig(awsCfg),
		Route53: route53.NewFromConfig(awsCfg),
		IAM:     iam.NewFromConfig(awsCfg),
		S3:      s3.NewFromConfig(awsCfg),
		Config:  cfg,
		Now:     time.Now,
	}, nil
}

func openReportFile(outputDir, prefix string) (io.Writer, func(), error) {
	if outputDir == "" {
		return io.Discard, func() {}, nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating output dir: %w", err)
	}

	ts := time.Now().Format("2006-01-02T15-04-05")
	filename := filepath.Join(outputDir, fmt.Sprintf("%s-%s.txt", prefix, ts))

	f, err := os.Create(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("creating report file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Report file: %s\n", filename)
	return f, func() { f.Close() }, nil
}

func tee(a, b io.Writer) io.Writer {
	return io.MultiWriter(a, b)
}

// promptYN asks a yes/no question on stderr and reads from stdin.
func promptYN(question string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(answer)), "y")
}

func runScan(ctx context.Context, cfg Config) error {
	scanner, err := newScanner(ctx, cfg)
	if err != nil {
		return err
	}

	fileW, closeFile, err := openReportFile(cfg.OutputDir, "scan")
	if err != nil {
		return err
	}
	defer closeFile()

	fmt.Fprintf(os.Stderr, "Scanning VPCs in %s (min-age: %s)...\n", cfg.Region, cfg.MinAge)

	results, err := scanner.Scan(ctx)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Correlating with Prow data...\n")
	CorrelateWithProw(ctx, scanner.Route53, results)

	out := tee(os.Stdout, fileW)

	switch cfg.OutputFormat {
	case "json":
		if err := PrintJSON(out, results); err != nil {
			return err
		}
	default:
		PrintTable(out, results)
		PrintSummary(out, results)
	}

	return nil
}

func runDelete(ctx context.Context, cfg Config) error {
	scanner, err := newScanner(ctx, cfg)
	if err != nil {
		return err
	}

	prefix := "delete"
	if cfg.DryRun {
		prefix = "dry-run"
	} else if cfg.Interactive {
		prefix = "interactive-delete"
	}
	fileW, closeFile, err := openReportFile(cfg.OutputDir, prefix)
	if err != nil {
		return err
	}
	defer closeFile()

	fmt.Fprintf(os.Stderr, "Scanning VPCs in %s (min-age: %s)...\n", cfg.Region, cfg.MinAge)

	results, err := scanner.Scan(ctx)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Correlating with Prow data...\n")
	CorrelateWithProw(ctx, scanner.Route53, results)

	out := tee(os.Stdout, fileW)

	PrintTable(out, results)
	PrintSummary(out, results)

	leaked := FilterLeaked(results)
	if len(leaked) == 0 {
		fmt.Fprintln(out, "\nNo leaked resources to delete.")
		return nil
	}

	if cfg.Limit > 0 && len(leaked) > cfg.Limit {
		fmt.Fprintf(out, "\nLimiting to %d of %d leaked infra sets (--limit %d).\n", cfg.Limit, len(leaked), cfg.Limit)
		leaked = leaked[:cfg.Limit]
	}

	totalVPCs := 0
	totalZones := 0
	for _, s := range leaked {
		totalVPCs += len(s.VPCs)
		totalZones += len(s.HostedZones)
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "\n[DRY RUN] Would delete %d infra sets (%d VPCs, %d zones).\n", len(leaked), totalVPCs, totalZones)
		return nil
	}

	deleter := &Deleter{
		EC2:     scanner.EC2,
		ELBv2:   scanner.ELBv2,
		Route53: scanner.Route53,
		IAM:     scanner.IAM,
	}

	// Mode 1: --confirm → no prompts, delete everything
	if cfg.Confirm {
		return deleter.DeleteAll(ctx, leaked, ConfirmAll)
	}

	// Mode 2: --interactive → prompt per resource
	if cfg.Interactive {
		return deleter.DeleteAll(ctx, leaked, ConfirmEach)
	}

	// Mode 3: no flags → prompt once at the start
	fmt.Fprintf(os.Stderr, "\nWARNING: This will delete %d infra sets (%d VPCs, %d zones) and ALL their sub-resources.\n", len(leaked), totalVPCs, totalZones)
	if !promptYN("Proceed with deletion?") {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return nil
	}

	return deleter.DeleteAll(ctx, leaked, ConfirmAll)
}
