package agent

import (
	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/core"
	cmdutil "github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Agent",
		SilenceUsage: true,
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := agent.DestroyCluster(cmd.Context(), opts); err != nil {
			cmdutil.NewLogger().Error(err, "Failed to destroy cluster")
			return err
		}

		return nil
	}

	return cmd
}
