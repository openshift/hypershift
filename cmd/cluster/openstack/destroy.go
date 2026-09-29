package openstack

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Destroys a HostedCluster and its associated infrastructure on OpenStack",
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

		if err := DestroyCluster(ctx, opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	return destroyCluster(ctx, o, core.GetCluster, core.DestroyCluster)
}

// getClusterFunc resolves the HostedCluster to destroy. It is a parameter of
// destroyCluster so tests can exercise the destroy logic without a cluster.
type getClusterFunc func(ctx context.Context, o *core.DestroyOptions) (*hyperv1.HostedCluster, error)

// coreDestroyFunc performs the platform-agnostic destroy. It is a parameter of
// destroyCluster so tests can observe what is handed to the core destroy path.
type coreDestroyFunc func(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *core.DestroyOptions, destroyPlatformSpecifics core.DestroyPlatformSpecifics) error

func destroyCluster(ctx context.Context, o *core.DestroyOptions, getCluster getClusterFunc, coreDestroy coreDestroyFunc) error {
	hostedCluster, err := getCluster(ctx, o)
	if err != nil {
		return err
	}

	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
	}

	var inputErrors []error
	if o.InfraID == "" {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	return coreDestroy(ctx, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	return nil
}
