package openstack

import (
	"github.com/openshift/hypershift/cmd/nodepool/core"
	openstacknodepool "github.com/openshift/hypershift/cmd/nodepool/openstack"

	"github.com/spf13/cobra"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := openstacknodepool.DefaultOptions()
	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Creates basic functional NodePool resources for OpenStack platform",
		SilenceUsage: true,
	}
	openstacknodepool.BindOptions(platformOpts, cmd.Flags())
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
