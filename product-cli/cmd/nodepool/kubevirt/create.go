package kubevirt

import (
	"github.com/openshift/hypershift/cmd/nodepool/core"
	kubevirtnodepool "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions, clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := util.ResolveClientProvider(clientProviders...)
	platformOpts := kubevirtnodepool.DefaultOptions()
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional NodePool resources for KubeVirt platform",
		SilenceUsage: true,
	}
	kubevirtnodepool.BindOptions(platformOpts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		validOpts, err := platformOpts.Validate(ctx, coreOpts)
		if err != nil {
			return err
		}

		opts, err := validOpts.Complete(ctx, coreOpts)
		if err != nil {
			return err
		}
		return coreOpts.CreateRunFunc(opts, clientProvider)(cmd, args)
	}

	return cmd
}
