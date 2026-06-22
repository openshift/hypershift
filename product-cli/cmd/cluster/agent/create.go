package agent

import (
	"context"

	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/core"

	"github.com/spf13/cobra"
)

func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	agentOpts := agent.DefaultOptions()
	agent.BindOptions(agentOpts, cmd.Flags())
	_ = cmd.MarkFlagRequired("agent-namespace")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := core.CreateCluster(ctx, opts, agentOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}
