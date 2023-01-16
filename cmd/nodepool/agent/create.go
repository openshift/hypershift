package agent

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AgentPlatformCreateOptions struct{}

func NewAgentPlatformCreateOptions() *AgentPlatformCreateOptions {
	platformOpts := &AgentPlatformCreateOptions{}

	return platformOpts
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional NodePool resources for Agent platform",
		SilenceUsage: true,
	}

	platformOpts := NewAgentPlatformCreateOptions()

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *AgentPlatformCreateOptions) UpdateNodePool(context.Context, *hyperv1.NodePool, *hyperv1.HostedCluster, crclient.Client) error {
	return nil
}

func (o *AgentPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AgentPlatform
}

func (o *AgentPlatformCreateOptions) Validate() error {
	return nil
}
