package kubevirt

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/spf13/cobra"
)

type DestroyOptions struct {
	Namespace          string
	Name               string
	ClusterGracePeriod time.Duration
}

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Kubevirt platform.",
		SilenceUsage: true,
	}

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := none.DestroyCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}
