package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

// NewCreateCommand creates the Azure IAM create command for the product CLI
func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates Azure managed identities and federated credentials for a HostedCluster",
		SilenceUsage: true,
	}

	opts := hypershiftazure.DefaultCreateIAMOptions()
	hypershiftazure.BindCreateIAMProductFlags(opts, cmd.Flags())

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("resource-group-name")
	_ = cmd.MarkFlagRequired("oidc-issuer-url")
	_ = cmd.MarkFlagRequired("output-file")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to create IAM resources")
			return err
		}
		l.Info("Successfully created IAM resources")
		return nil
	}

	return cmd
}
