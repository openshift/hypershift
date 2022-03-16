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
	o := &opts{
		instanceType: "Standard_D4s_v4",
		diskSize:     120,
	}
	cmd.Flags().StringVar(&o.instanceType, "instance-type", o.instanceType, "The instance type to use for the nodepool")
	cmd.Flags().Int32Var(&o.diskSize, "root-disk-size", o.diskSize, "The size of the root disk for machines in the NodePool (minimum 16)")
	cmd.Flags().StringVar(&o.availabilityZone, "availability-zone", o.availabilityZone, "The availabilityZone for the nodepool. Must be left unspecified if in a region that doesn't support AZs")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := coreOpts.CreateNodePool(ctx, o); err != nil {
			log.Log.Error(err, "Failed to create nodepool")
			os.Exit(1)
		}
	}

	return cmd
}

type opts struct {
	instanceType     string
	diskSize         int32
	availabilityZone string
}

func (o *opts) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error {
	nodePool.Spec.Platform.Type = hyperv1.AzurePlatform
	nodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
		VMSize:           o.instanceType,
		DiskSizeGB:       o.diskSize,
		AvailabilityZone: o.availabilityZone,
	}
	return nil
}

func (o opts) Type() hyperv1.PlatformType {
	return hyperv1.AzurePlatform
}
