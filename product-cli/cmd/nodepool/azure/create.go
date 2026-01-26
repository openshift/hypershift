package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	"github.com/spf13/cobra"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := hypershiftazure.DefaultOptions()
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional NodePool resources for Azure platform",
		SilenceUsage: true,
	}

	hypershiftazure.BindProductFlags(platformOpts, cmd.Flags())

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
		return coreOpts.CreateRunFunc(opts)(cmd, args)
	}

	return cmd
}
