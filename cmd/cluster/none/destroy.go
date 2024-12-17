package none

import (
	"context"
	"fmt"

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
	hostedCluster, err := core.GetCluster(ctx, o)
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
	return core.DestroyCluster(ctx, hostedCluster, o, nil)
}
