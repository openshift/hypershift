package openstack

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	"k8s.io/apimachinery/pkg/util/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Destroys a HostedCluster and its associated infrastructure on OpenStack",
		SilenceUsage: true,
	}

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT)
		defer stop()

		client, err := clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
		if err != nil {
			logger.Error(err, "Failed to create management cluster client")
			return err
		}

		if err := DestroyCluster(ctx, opts, client); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
	}

	return cmd
}
func DestroyCluster(ctx context.Context, o *core.DestroyOptions, client crclient.Client) error {
	hostedCluster, err := core.GetCluster(ctx, client, o)
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

	return core.DestroyCluster(ctx, client, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions, _ crclient.Client) error {
	return nil
}
