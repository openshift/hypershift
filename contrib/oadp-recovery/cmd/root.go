package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// NewRootCmd creates the root command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oadp-recovery",
		Short: "OADP recovery tool for HyperShift clusters",
		Long: `A tool that automatically detects and recovers HyperShift clusters
that are paused due to OADP (OpenShift API for Data Protection) backup operations.
It monitors Velero backup states and resumes clusters when backups reach terminal states.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newRunCmd())
	return cmd
}

// Execute runs the root command.
func Execute(ctx context.Context) {
	logger := zap.New()
	ctrl.SetLogger(logger)
	if err := NewRootCmd().ExecuteContext(ctx); err != nil {
		logger.Error(err, "command execution failed")
		os.Exit(1)
	}
}