package none

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type NonePlatformCreateOptions struct{}

func NewNonePlatformCreateOptions(cmd *cobra.Command) *NonePlatformCreateOptions {
	platformOpts := &NonePlatformCreateOptions{}

	return platformOpts
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Creates basic functional NodePool resources for None platform",
		SilenceUsage: true,
	}

	platformOpts := NewNonePlatformCreateOptions(cmd)

	cmd.RunE = coreOpts.CreateExecFunc(platformOpts)

	return cmd
}

func (o *NonePlatformCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error {
	return nil
}

func (o *NonePlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.NonePlatform
}

func (o *NonePlatformCreateOptions) Validate() error {
	return nil
}
