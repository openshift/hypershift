package openstack

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Destroys a HostedCluster and its associated infrastructure on OpenStack platform",
		SilenceUsage: true,
	}

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		client, err := clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
		if err != nil {
			logger.Error(err, "Failed to create management cluster client")
			return err
		}

		if err := openstack.DestroyCluster(ctx, opts, client); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
	}

	return cmd
}
