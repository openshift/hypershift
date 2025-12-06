package gcp

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type DestroyIAMOptions struct {
	ProjectID string
	InfraID   string
}

func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys GCP IAM infrastructure for HyperShift cluster",
		SilenceUsage: true,
	}

	opts := &DestroyIAMOptions{}

	cmd.Flags().StringVar(&opts.InfraID, infraIDFlag, opts.InfraID, "Infrastructure ID used for GCP resources.")
	cmd.Flags().StringVar(&opts.ProjectID, projectIDFlag, opts.ProjectID, "GCP Project ID where resources were created")

	_ = cmd.MarkFlagRequired(infraIDFlag)
	_ = cmd.MarkFlagRequired(projectIDFlag)

	logger := log.Log
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		return opts.ValidateInputs()
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to destroy GCP IAM infrastructure")
			return err
		}
		logger.Info("Successfully destroyed GCP IAM infrastructure")
		return nil
	}

	return cmd
}

func (o *DestroyIAMOptions) ValidateInputs() error {
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.ProjectID == "" {
		return fmt.Errorf("project-id is required")
	}
	return nil
}

func (o *DestroyIAMOptions) Run(ctx context.Context, logger logr.Logger) error {
	return o.DestroyIAM(ctx, logger)
}

func (o *DestroyIAMOptions) DestroyIAM(ctx context.Context, logger logr.Logger) error {
	// Use IAMManager for all GCP API interactions
	iamManager, err := NewIAMManager(ctx, o.ProjectID, o.InfraID, "", logger)
	if err != nil {
		return fmt.Errorf("failed to initialize GCP clients: %w", err)
	}

	// Delete service accounts first (they reference the WIF pool)
	logger.Info("Deleting service accounts...")
	if err := iamManager.DeleteServiceAccounts(ctx); err != nil {
		return fmt.Errorf("failed to delete service accounts: %w", err)
	}

	// Delete OIDC provider (it references the WIF pool)
	logger.Info("Deleting OIDC provider...")
	if err := iamManager.DeleteOIDCProvider(ctx); err != nil {
		return fmt.Errorf("failed to delete OIDC provider: %w", err)
	}

	// Delete workload identity pool last
	logger.Info("Deleting workload identity pool...")
	if err := iamManager.DeleteWorkloadIdentityPool(ctx); err != nil {
		return fmt.Errorf("failed to delete workload identity pool: %w", err)
	}

	logger.Info("Destroyed GCP IAM infrastructure", "infraID", o.InfraID, "projectID", o.ProjectID)
	return nil
}
