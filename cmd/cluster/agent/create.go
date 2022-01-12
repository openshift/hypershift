package agent

import (
	"context"
	"fmt"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	nodepoolagent "github.com/openshift/hypershift/cmd/nodepool/agent"
	nodepoolcore "github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

type AgentPlatformCreateOptions struct {
	APIServerAddress string
	AgentNamespace   string
	NodePoolOptions  *nodepoolagent.AgentPlatformCreateOptions
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	platformOpts := &AgentPlatformCreateOptions{
		AgentNamespace: "",
	}

	cmd.Flags().StringVar(&platformOpts.AgentNamespace, "agent-namespace", platformOpts.AgentNamespace, "The namespace in which to search for Agents")
	cmd.MarkFlagRequired("agent-namespace")

	platformOpts.NodePoolOptions = nodepoolagent.NewAgentPlatformCreateOptions(cmd)

	cmd.RunE = opts.CreateExecFunc(platformOpts)

	return cmd
}

func (o *AgentPlatformCreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, name, infraID, baseDomain string) (err error) {
	if o.APIServerAddress == "" {
		o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx)
		if err != nil {
			return err
		}
	}

	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = baseDomain
	if exampleOptions.BaseDomain == "" {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.Agent = &apifixtures.ExampleAgentOptions{
		APIServerAddress: o.APIServerAddress,
		AgentNamespace:   o.AgentNamespace,
	}
	return nil
}

func (o *AgentPlatformCreateOptions) NodePoolPlatformOptions() nodepoolcore.PlatformOptions {
	return o.NodePoolOptions
}

func (o *AgentPlatformCreateOptions) Validate() error {
	return o.NodePoolOptions.Validate()
}
