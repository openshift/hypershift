package agent

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AgentPlatformCreateOptions struct{}

func NewAgentPlatformCreateOptions(cmd *cobra.Command) *AgentPlatformCreateOptions {
	platformOpts := &AgentPlatformCreateOptions{}

	return platformOpts
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional NodePool resources for Agent platform",
		SilenceUsage: true,
	}

	platformOpts := NewAgentPlatformCreateOptions(cmd)

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *AgentPlatformCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error {
	return nil
}

func (o *AgentPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AgentPlatform
}

func (o *AgentPlatformCreateOptions) Validate() error {
	return nil
}
