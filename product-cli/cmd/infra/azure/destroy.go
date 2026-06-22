package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	cmdutil "github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

// NewDestroyCommand creates the Azure infrastructure destroy command for the product CLI
func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys Azure infrastructure resources for a HostedCluster",
		SilenceUsage: true,
	}

	opts := hypershiftazure.DefaultDestroyOptions()
	hypershiftazure.BindDestroyProductFlags(opts, cmd.Flags())

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("name")

	l := cmdutil.NewLogger()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to destroy infrastructure")
			return err
		}
		l.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}
