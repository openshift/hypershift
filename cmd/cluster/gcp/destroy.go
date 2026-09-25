package gcp

import (
	"context"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

// NewDestroyCommand creates a new cobra command for destroying GCP clusters
func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys a GCP HostedCluster",
		SilenceUsage: true,
	}

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
		if err != nil {
			return err
		}
		if err := DestroyCluster(cmd.Context(), opts, client); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
	}

	return cmd
}

// DestroyCluster destroys a GCP HostedCluster and its associated infrastructure
func DestroyCluster(ctx context.Context, destroyOptions *core.DestroyOptions, client crclient.Client) error {
	hostedCluster, err := core.GetCluster(ctx, client, destroyOptions)
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
	return core.DestroyCluster(ctx, client, hostedCluster, destroyOptions, nil)
}
