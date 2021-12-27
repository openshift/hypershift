package none

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/errors"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Destroys a HostedCluster and its associated infrastructure on None.",
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

		if err := DestroyCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to destroy cluster")
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
	}
	var inputErrors []error
	if len(o.InfraID) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}
	return core.DestroyCluster(ctx, hostedCluster, o, core.DestroyPlatformSpecificsNoop)
}
