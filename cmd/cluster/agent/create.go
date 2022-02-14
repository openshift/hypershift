package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

type CreateOptions struct {
	APIServerAddress string
	AgentNamespace   string
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	platformOpts := CreateOptions{
		AgentNamespace: "",
	}

	cmd.Flags().StringVar(&platformOpts.AgentNamespace, "agent-namespace", platformOpts.AgentNamespace, "The namespace in which to search for Agents")
	cmd.MarkFlagRequired("agent-namespace")

	cmd.RunE = opts.CreateRunFunc(&platformOpts)

	return cmd
}

func (o *CreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if o.APIServerAddress == "" {
		o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx)
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
		APIServerAddress: o.APIServerAddress,
		AgentNamespace:   o.AgentNamespace,
	}
	return nil
}
