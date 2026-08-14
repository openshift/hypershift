package infra

import (
	"github.com/openshift/hypershift/product-cli/cmd/infra/azure"

	"github.com/spf13/cobra"
)

// NewCreateCommands creates the infrastructure create command with platform subcommands
func NewCreateCommands() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "infra",
		Short:        "Commands for creating infrastructure resources for HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(azure.NewCreateCommand())

	return cmd
}

// NewDestroyCommands creates the infrastructure destroy command with platform subcommands
func NewDestroyCommands() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "infra",
		Short:        "Commands for destroying infrastructure resources for HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(azure.NewDestroyCommand())

	return cmd
}
