package agent

import (
	"context"
	"time"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/log"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

type DestroyOptions struct {
	Namespace          string
	Name               string
	ClusterGracePeriod time.Duration
}

func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Agent",
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

func DestroyCluster(ctx context.Context, o *core.DestroyOptions, client crclient.Client) error {
	return none.DestroyCluster(ctx, o, client)
}
