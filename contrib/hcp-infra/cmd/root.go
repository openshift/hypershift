package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// GlobalFlags holds flags shared across all subcommands.
type GlobalFlags struct {
	Regions    []string
	Kubeconfig string
	Output     string
	DryRun     bool
	Verbose    bool
	Quiet      bool
}

// NewRootCommand creates the root cobra command.
func NewRootCommand(version string) *cobra.Command {
	flags := &GlobalFlags{}

	cmd := &cobra.Command{
		Use:          "hcp-infra",
		Short:        "HyperShift infrastructure management",
		Long:         "Scan, audit, and clean orphaned AWS infrastructure from HyperShift test clusters.",
		SilenceUsage: true,
	}

	// Global flags
	cmd.PersistentFlags().StringSliceVar(&flags.Regions, "region", defaultRegions(), "AWS regions to scan")
	cmd.PersistentFlags().StringVar(&flags.Kubeconfig, "kubeconfig", "", "Management cluster kubeconfig (for HC cross-reference)")
	cmd.PersistentFlags().StringVarP(&flags.Output, "output", "o", "text", "Output format: text, json, markdown")
	cmd.PersistentFlags().BoolVar(&flags.DryRun, "dry-run", false, "Preview actions without making changes")
	cmd.PersistentFlags().BoolVarP(&flags.Verbose, "verbose", "v", false, "Verbose output")
	cmd.PersistentFlags().BoolVarP(&flags.Quiet, "quiet", "q", false, "Quiet output (summary only)")

	// Add subcommands
	cmd.AddCommand(NewScanCommand(flags))
	cmd.AddCommand(NewCleanCommand(flags))
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(c *cobra.Command, args []string) {
			c.Println("hcp-infra version", version)
		},
	})

	return cmd
}

func defaultRegions() []string {
	if env := os.Getenv("HCP_INFRA_REGIONS"); env != "" {
		return strings.Split(env, ",")
	}
	return []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2", "eu-west-1"}
}
