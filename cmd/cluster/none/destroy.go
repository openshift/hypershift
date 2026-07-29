package none

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Destroys a HostedCluster and its associated infrastructure on None",
		SilenceUsage: true,
	}

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := DestroyCluster(cmd.Context(), opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
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
	if len(o.InfraID) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}
	// The None platform has no infrastructure of its own, so it passes nil for
	// the platform specifics hook; core.DestroyCluster then waits for the
	// HostedCluster to be deleted instead of running a cleanup step.
	return coreDestroy(ctx, hostedCluster, o, nil)
}
