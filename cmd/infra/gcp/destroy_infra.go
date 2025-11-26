package gcp

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// DestroyInfraOptions contains options for destroying GCP infrastructure
type DestroyInfraOptions struct {
	// Required flags
	ProjectID string
	Region    string
	InfraID   string
}

// NewDestroyCommand creates a new cobra command for destroying GCP infrastructure
func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys GCP infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := DestroyInfraOptions{}

	cmd.Flags().StringVar(&opts.ProjectID, "project-id", opts.ProjectID, "GCP Project ID (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "GCP region where infrastructure was created (required)")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID used for resource naming (required)")

	_ = cmd.MarkFlagRequired("project-id")
	_ = cmd.MarkFlagRequired("region")
	_ = cmd.MarkFlagRequired("infra-id")

	logger := log.Log
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		return opts.Validate()
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to destroy GCP infrastructure")
			return err
		}
		logger.Info("Successfully destroyed GCP infrastructure")
		return nil
	}

	return cmd
}

// Validate validates the destroy infrastructure options
func (o *DestroyInfraOptions) Validate() error {
	if o.ProjectID == "" {
		return fmt.Errorf("--project-id is required")
	}
	if o.InfraID == "" {
		return fmt.Errorf("--infra-id is required")
	}
	if o.Region == "" {
		return fmt.Errorf("--region is required")
	}
	return nil
}

// Run executes the infrastructure destruction
func (o *DestroyInfraOptions) Run(ctx context.Context, logger logr.Logger) error {
	logger.Info("Destroying GCP infrastructure", "projectID", o.ProjectID, "region", o.Region, "infraID", o.InfraID)

	// Initialize network manager
	networkManager, err := NewNetworkManager(ctx, o.ProjectID, o.InfraID, o.Region, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize network manager: %w", err)
	}

	// Delete resources in reverse order of creation (dependencies first)
	// Order: Firewall -> NAT -> Router -> Subnet -> Network

	// Delete egress firewall rule
	if err := networkManager.DeleteEgressFirewall(ctx); err != nil {
		return fmt.Errorf("failed to delete egress firewall rule: %w", err)
	}

	// Delete Cloud NAT (by updating router to remove NAT config)
	if err := networkManager.DeleteNAT(ctx); err != nil {
		return fmt.Errorf("failed to delete Cloud NAT: %w", err)
	}

	// Delete Cloud Router
	if err := networkManager.DeleteRouter(ctx); err != nil {
		return fmt.Errorf("failed to delete Cloud Router: %w", err)
	}

	// Delete subnet
	if err := networkManager.DeleteSubnet(ctx); err != nil {
		return fmt.Errorf("failed to delete subnet: %w", err)
	}

	// Delete VPC network
	if err := networkManager.DeleteNetwork(ctx); err != nil {
		return fmt.Errorf("failed to delete VPC network: %w", err)
	}

	return nil
}