package agent

import (
	"context"

	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/spf13/cobra"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	opts.AgentPlatform = core.AgentPlatformCreateOptions{
		APIServerAddress:   "",
		AgentNamespace:     "",
		AgentLabelSelector: "",
	}

	cmd.Flags().StringVar(&opts.AgentPlatform.APIServerAddress, "api-server-address", opts.AgentPlatform.APIServerAddress, "The IP address to be used for the hosted cluster's Kubernetes API communication. Requires management cluster connectivity if left unset.")
	cmd.Flags().StringVar(&opts.AgentPlatform.AgentNamespace, "agent-namespace", opts.AgentPlatform.AgentNamespace, "The namespace in which to search for Agents")
	cmd.Flags().StringVar(&opts.AgentPlatform.AgentLabelSelector, "agentLabelSelector", opts.AgentPlatform.AgentLabelSelector, "A LabelSelector used to select Agents according to their labels, in JSON format")
	_ = cmd.MarkFlagRequired("agent-namespace")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := createCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func createCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, agent.ApplyPlatformSpecificsValues)
}
