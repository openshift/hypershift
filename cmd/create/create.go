package create

import (
	"github.com/openshift/hypershift/cmd/bastion"
	"github.com/openshift/hypershift/cmd/cluster"
	"github.com/openshift/hypershift/cmd/infra"
	"github.com/openshift/hypershift/cmd/kubeconfig"
	"github.com/openshift/hypershift/cmd/nodepool"
	"github.com/openshift/hypershift/cmd/oadp"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := util.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HyperShift resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(oadp.NewCreateBackupCommand(clientProvider))
	cmd.AddCommand(oadp.NewCreateRestoreCommand(clientProvider))
	cmd.AddCommand(oadp.NewCreateScheduleCommand(clientProvider))
	cmd.AddCommand(cluster.NewCreateCommands(clientProvider))
	cmd.AddCommand(infra.NewCreateCommand())
	cmd.AddCommand(infra.NewCreateIAMCommand(clientProvider))
	cmd.AddCommand(infra.NewCreateOperatorRolesCommand(clientProvider))
	cmd.AddCommand(kubeconfig.NewCreateCommand(clientProvider))
	cmd.AddCommand(nodepool.NewCreateCommand(clientProvider))
	cmd.AddCommand(bastion.NewCreateCommand(clientProvider))

	return cmd
}
