package agent

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/log"
)

type DestroyOptions struct {
	Namespace          string
	Name               string
	ClusterGracePeriod time.Duration
}

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Agent.",
		SilenceUsage: true,
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := DestroyCluster(cmd.Context(), opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			return err
		}

		return nil
	}

	return cmd
}

func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	return none.DestroyCluster(ctx, o)
}
