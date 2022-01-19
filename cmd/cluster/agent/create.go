package agent

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
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

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		if opts.Timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), opts.Timeout)
		}
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
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
	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
	}
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
