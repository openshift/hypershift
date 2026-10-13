package agent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

type AgentPlatformCreateOptions struct {
	AgentLabelSelector string
}

func NewAgentPlatformCreateOptions(_ *cobra.Command) *AgentPlatformCreateOptions {
	platformOpts := &AgentPlatformCreateOptions{
		AgentLabelSelector: "",
	}

	return platformOpts
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional NodePool resources for Agent platform",
		SilenceUsage: true,
	}

	platformOpts := NewAgentPlatformCreateOptions(cmd)
	cmd.Flags().StringVar(&platformOpts.AgentLabelSelector, "agentLabelSelector", platformOpts.AgentLabelSelector, "A LabelSelector for selecting Agents according to their labels, e.g., 'size=large,zone notin (az1,az2)'")
	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *AgentPlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	agentSelector, err := metav1.ParseToLabelSelector(o.AgentLabelSelector)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse AgentLabelSelector: %s", err))
	}
	nodePool.Spec.Platform.Agent = &hyperv1.AgentNodePoolPlatform{
		AgentLabelSelector: agentSelector,
	}
	return nil
}

func (o *AgentPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AgentPlatform
}
