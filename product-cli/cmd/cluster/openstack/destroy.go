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

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Destroys a HostedCluster and its associated infrastructure on OpenStack platform",
		SilenceUsage: true,
	}

	logger := log.Log
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := openstack.DestroyCluster(ctx, opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}
