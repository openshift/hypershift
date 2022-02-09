package azure

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates an Azure nodepool",
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

		if err := coreOpts.CreateNodePool(ctx, opts{}); err != nil {
			log.Log.Error(err, "Failed to create nodepool")
			os.Exit(1)
		}
	}

	return cmd
}

type opts struct{}

func (opts) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error {
	return nil
}

func (o opts) Type() hyperv1.PlatformType {
	return hyperv1.AzurePlatform
}
