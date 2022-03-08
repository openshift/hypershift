package agent

import (
	"context"

	"github.com/spf13/cobra"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	opts.AgentPlatform = core.AgentPlatformCreateOptions{
		AgentNamespace: "",
	}

	cmd.Flags().StringVar(&opts.AgentPlatform.AgentNamespace, "agent-namespace", opts.AgentPlatform.AgentNamespace, "The namespace in which to search for Agents")
	cmd.MarkFlagRequired("agent-namespace")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts); err != nil {
			log.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if opts.AgentPlatform.APIServerAddress == "" {
		opts.AgentPlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx)
		if err != nil {
			return err
		}
	}

	infraID := opts.InfraID
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = opts.BaseDomain
	if exampleOptions.BaseDomain == "" {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.Agent = &apifixtures.ExampleAgentOptions{
		APIServerAddress: opts.AgentPlatform.APIServerAddress,
		AgentNamespace:   opts.AgentPlatform.AgentNamespace,
	}
	return nil
}
