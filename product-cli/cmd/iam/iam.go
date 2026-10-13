package iam

import (
	"github.com/openshift/hypershift/product-cli/cmd/iam/azure"

	"github.com/spf13/cobra"
)

// NewCreateCommands creates the IAM create command with platform subcommands
func NewCreateCommands() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "iam",
		Short:        "Commands for creating IAM resources for HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(azure.NewCreateCommand())

	return cmd
}

// NewDestroyCommands creates the IAM destroy command with platform subcommands
func NewDestroyCommands() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "iam",
		Short:        "Commands for destroying IAM resources for HostedClusters",
		SilenceUsage: true,
	}

	cmd.AddCommand(azure.NewDestroyCommand())

	return cmd
}
