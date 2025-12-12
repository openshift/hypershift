package fix

import (
	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "fix",
		Short:        "Commands for fixing HyperShift cluster issues",
		Long: `Commands for fixing various HyperShift cluster issues.

This command group provides tools to diagnose and fix common problems
that can occur with HyperShift clusters during operation or disaster
recovery scenarios.`,
		Example: `
  # Fix OIDC identity provider issues during disaster recovery
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials

  # List available fix commands
  hypershift fix --help`,
		SilenceUsage: true,
	}

	cmd.AddCommand(NewDrOidcIamCommand())

	return cmd
}