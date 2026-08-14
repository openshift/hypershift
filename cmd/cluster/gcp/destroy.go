package gcp

import (
	"context"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

// NewDestroyCommand creates a new cobra command for destroying GCP clusters
func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys a GCP HostedCluster",
		SilenceUsage: true,
	}

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

// DestroyCluster destroys a GCP HostedCluster and its associated infrastructure
func DestroyCluster(ctx context.Context, destroyOptions *core.DestroyOptions) error {
	hostedCluster, err := core.GetCluster(ctx, destroyOptions)
	if err != nil {
		return err
	}
	if hostedCluster != nil && hostedCluster.Spec.Platform.GCP != nil {
		destroyOptions.InfraID = hostedCluster.Spec.InfraID
		// Set GCP-specific options from the HostedCluster if available
		// Currently, there are no GCP-specific destroy options, but this is where they would be set
	}

	// For now, GCP cluster destruction only removes the HostedCluster resource
	// Additional GCP infrastructure cleanup logic can be added here in the future
	return core.DestroyCluster(ctx, hostedCluster, destroyOptions, nil)
}
