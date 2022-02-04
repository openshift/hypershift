package powervs

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/hypershift/cmd/cluster/core"
	powervsinfra "github.com/openshift/hypershift/cmd/infra/powervs"
	"github.com/openshift/hypershift/cmd/log"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Destroys a HostedCluster and its resources on PowerVS",
		SilenceUsage: true,
	}
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := DestroyCluster(ctx, opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}
	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
		o.PowerVSPlatform.ResourceGroup = hostedCluster.Spec.Platform.PowerVS.ResourceGroup
		o.PowerVSPlatform.Region = hostedCluster.Spec.Platform.PowerVS.Region
		o.PowerVSPlatform.Zone = hostedCluster.Spec.Platform.PowerVS.Zone
		o.PowerVSPlatform.VPCRegion = hostedCluster.Spec.Platform.PowerVS.VPC.Region
	}

	var inputErrors []error
	if o.InfraID == "" {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if o.PowerVSPlatform.Region == "" {
		inputErrors = append(inputErrors, fmt.Errorf("PowerVS region is required"))
	}
	if o.PowerVSPlatform.Zone == "" {
		inputErrors = append(inputErrors, fmt.Errorf("PowerVS zone is required"))
	}
	if o.PowerVSPlatform.VPCRegion == "" {
		inputErrors = append(inputErrors, fmt.Errorf("VPC region is required"))
	}
	if o.PowerVSPlatform.ResourceGroup == "" {
		inputErrors = append(inputErrors, fmt.Errorf("resource group is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	return (&powervsinfra.DestroyInfraOptions{
		InfraID:       o.InfraID,
		ResourceGroup: o.PowerVSPlatform.ResourceGroup,
		PowerVSRegion: o.PowerVSPlatform.Region,
		PowerVSZone:   o.PowerVSPlatform.Zone,
		VpcRegion:     o.PowerVSPlatform.VPCRegion,
	}).Run(ctx)
}
