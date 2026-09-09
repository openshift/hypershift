package agent

import (
	hypershiftagent "github.com/openshift/hypershift/cmd/nodepool/agent"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions, clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := util.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional NodePool resources for Agent platform",
		SilenceUsage: true,
	}

	platformOpts := hypershiftagent.NewAgentPlatformCreateOptions(cmd)
	cmd.Flags().StringVar(&platformOpts.AgentLabelSelector, "agentLabelSelector", platformOpts.AgentLabelSelector, "A LabelSelector for selecting Agents according to their labels, e.g., 'size=large,zone notin (az1,az2)'")
	cmd.RunE = coreOpts.CreateRunFunc(platformOpts, clientProvider)

	return cmd
}
