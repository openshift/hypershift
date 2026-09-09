package create

import (
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/oadp"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/product-cli/cmd/cluster"
	"github.com/openshift/hypershift/product-cli/cmd/iam"
	"github.com/openshift/hypershift/product-cli/cmd/infra"
	"github.com/openshift/hypershift/product-cli/cmd/kubeconfig"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool"

	"github.com/spf13/cobra"
)

func NewCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(oadp.NewCreateBackupCommand(clientProvider))
	cmd.AddCommand(oadp.NewCreateRestoreCommand(clientProvider))
	cmd.AddCommand(oadp.NewCreateScheduleCommand(clientProvider))
	cmd.AddCommand(cluster.NewCreateCommands(clientProvider))
	cmd.AddCommand(iam.NewCreateCommands())
	cmd.AddCommand(infra.NewCreateCommands())
	cmd.AddCommand(kubeconfig.NewCreateCommand(clientProvider))
	cmd.AddCommand(nodepool.NewCreateCommand(clientProvider))

	return cmd
}
