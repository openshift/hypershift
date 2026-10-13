package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

// NewCreateCommand creates the Azure infrastructure create command for the product CLI
func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates Azure infrastructure resources for a HostedCluster",
		SilenceUsage: true,
	}

	opts := hypershiftazure.DefaultOptions()
	hypershiftazure.BindProductFlags(opts, cmd.Flags())

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("name")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if _, err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to create infrastructure")
			return err
		}
		l.Info("Successfully created infrastructure")
		return nil
	}

	return cmd
}
