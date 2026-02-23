package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

// NewDestroyCommand creates the Azure IAM destroy command for the product CLI
func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys Azure managed identities and federated credentials for a HostedCluster",
		SilenceUsage: true,
	}

	opts := hypershiftazure.DefaultDestroyIAMOptions()
	hypershiftazure.BindDestroyIAMProductFlags(opts, cmd.Flags())

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("workload-identities-file")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("resource-group-name")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to destroy IAM resources")
			return err
		}
		l.Info("Successfully destroyed IAM resources")
		return nil
	}

	return cmd
}
