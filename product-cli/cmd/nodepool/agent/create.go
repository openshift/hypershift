package agent

import (
	"github.com/spf13/cobra"

	hypershiftagent "github.com/openshift/hypershift/cmd/nodepool/agent"
	"github.com/openshift/hypershift/cmd/nodepool/core"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional NodePool resources for Agent platform",
		SilenceUsage: true,
	}

	platformOpts := hypershiftagent.NewAgentPlatformCreateOptions(cmd)
	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}
