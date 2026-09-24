package gcp

import (
	"context"
	"errors"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// Test stubs - production uses real implementation, tests can override
var (
	runDestroyIAM = func(ctx context.Context, opts gcpinfra.DestroyIAMOptions, log logr.Logger) error {
		return opts.Run(ctx, log)
	}
	runDestroyInfra = func(ctx context.Context, opts gcpinfra.DestroyInfraOptions, log logr.Logger) error {
		return opts.Run(ctx, log)
	}
)

// NewDestroyCommand creates a new cobra command for destroying GCP clusters
func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys a GCP HostedCluster and its associated infrastructure",
		SilenceUsage: true,
	}

	opts.GCPPlatform = core.GCPPlatformDestroyOptions{
		PreserveIAM:   false,
		PreserveInfra: false,
	}

	cmd.Flags().BoolVar(&opts.GCPPlatform.PreserveIAM, "preserve-iam", opts.GCPPlatform.PreserveIAM, "Skip deleting IAM resources (Workload Identity Pool, Service Accounts, OIDC Provider). Reuse requires same --infra-id.")
	cmd.Flags().BoolVar(&opts.GCPPlatform.PreserveInfra, "preserve-infra", opts.GCPPlatform.PreserveInfra, "Skip deleting infrastructure (VPC, subnet, router, NAT, firewall). Reuse requires same --infra-id.")
	cmd.Flags().StringVar(&opts.GCPPlatform.ProjectID, "project-id", opts.GCPPlatform.ProjectID, "GCP project ID; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.GCPPlatform.Region, "region", opts.GCPPlatform.Region, "GCP region; inferred from the hosted cluster by default")

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := DestroyCluster(cmd.Context(), opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
	}

	return cmd
}

// destroyPlatformSpecifics destroys GCP infrastructure and IAM resources.
// Destroy order mirrors reverse of creation: IAM first (identities/permissions layer), then infrastructure (base networking layer).
func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	var errs []error

	// Destroy IAM first (unless --preserve-iam is set)
	if !o.GCPPlatform.PreserveIAM {
		o.Log.Info("Destroying IAM")
		destroyIAMOpts := gcpinfra.DestroyIAMOptions{
			ProjectID: o.GCPPlatform.ProjectID,
			InfraID:   o.InfraID,
		}
		if err := runDestroyIAM(ctx, destroyIAMOpts, o.Log); err != nil {
			errs = append(errs, fmt.Errorf("failed to destroy IAM: %w", err))
		}
	} else {
		o.Log.Info("Skipping IAM destruction (preserve-iam flag set)")
	}

	// Destroy infrastructure last (unless --preserve-infra is set)
	if !o.GCPPlatform.PreserveInfra {
		o.Log.Info("Destroying GCP infrastructure")
		destroyInfraOpts := gcpinfra.DestroyInfraOptions{
			ProjectID: o.GCPPlatform.ProjectID,
			Region:    o.GCPPlatform.Region,
			InfraID:   o.InfraID,
		}
		if err := runDestroyInfra(ctx, destroyInfraOpts, o.Log); err != nil {
			errs = append(errs, fmt.Errorf("failed to destroy infrastructure: %w", err))
		}
	} else {
		o.Log.Info("Skipping infrastructure destruction (preserve-infra flag set)")
	}

	return errors.Join(errs...)
}

// extractParameters extracts GCP parameters from HostedCluster into DestroyOptions
func extractParameters(hostedCluster *hyperv1.HostedCluster, o *core.DestroyOptions) {
	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
		if hostedCluster.Spec.Platform.GCP != nil {
			o.GCPPlatform.ProjectID = hostedCluster.Spec.Platform.GCP.Project
			o.GCPPlatform.Region = hostedCluster.Spec.Platform.GCP.Region
		}
	}
}

// validateInputs validates required GCP destroy inputs
func validateInputs(o *core.DestroyOptions) error {
	var inputErrors []error
	if len(o.InfraID) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if len(o.GCPPlatform.ProjectID) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("project ID is required"))
	}
	if !o.GCPPlatform.PreserveInfra && len(o.GCPPlatform.Region) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("region is required"))
	}
	if err := errors.Join(inputErrors...); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}
	return nil
}

// DestroyCluster destroys a GCP HostedCluster and its associated infrastructure
func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}

	extractParameters(hostedCluster, o)

	if err := validateInputs(o); err != nil {
		return err
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}
