package create

import (
	"github.com/openshift/hypershift/cmd/oadp"
	"github.com/openshift/hypershift/product-cli/cmd/cluster"
	"github.com/openshift/hypershift/product-cli/cmd/iam"
	"github.com/openshift/hypershift/product-cli/cmd/infra"
	"github.com/openshift/hypershift/product-cli/cmd/kubeconfig"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Commands for creating HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(oadp.NewCreateBackupCommand())
	cmd.AddCommand(oadp.NewCreateRestoreCommand())
	cmd.AddCommand(oadp.NewCreateScheduleCommand())
	cmd.AddCommand(cluster.NewCreateCommands())
	cmd.AddCommand(iam.NewCreateCommands())
	cmd.AddCommand(infra.NewCreateCommands())
	cmd.AddCommand(kubeconfig.NewCreateCommand())
	cmd.AddCommand(nodepool.NewCreateCommand())

	return cmd
}
