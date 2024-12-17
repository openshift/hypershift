package create

import (
	"github.com/openshift/hypershift/cmd/bastion"
	"github.com/openshift/hypershift/cmd/cluster"
	"github.com/openshift/hypershift/cmd/infra"
	"github.com/openshift/hypershift/cmd/kubeconfig"
	"github.com/openshift/hypershift/cmd/nodepool"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HyperShift resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(cluster.NewCreateCommands())
	cmd.AddCommand(infra.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateIAMCommand())
	cmd.AddCommand(kubeconfig.NewCreateCommand())
	cmd.AddCommand(nodepool.NewCreateCommand())
	cmd.AddCommand(bastion.NewCreateCommand())

	return cmd
}
