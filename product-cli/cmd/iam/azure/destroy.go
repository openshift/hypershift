package azure

import (
	"context"

	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// destroyIAMFunc destroys the Azure IAM resources described by opts. It is a
// parameter of newDestroyCommand so tests can exercise the command's control
// flow -- flag binding, validation ordering and error propagation -- without
// reaching Azure.
type destroyIAMFunc func(ctx context.Context, opts *hypershiftazure.DestroyIAMOptions, l logr.Logger) error

// NewDestroyCommand creates the Azure IAM destroy command for the product CLI
func NewDestroyCommand() *cobra.Command {
	return newDestroyCommand(func(ctx context.Context, opts *hypershiftazure.DestroyIAMOptions, l logr.Logger) error {
		return opts.Run(ctx, l)
	})
}

func newDestroyCommand(destroyIAM destroyIAMFunc) *cobra.Command {
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
		if err := destroyIAM(cmd.Context(), opts, l); err != nil {
			l.Error(err, "Failed to destroy IAM resources")
			return err
		}
		l.Info("Successfully destroyed IAM resources")
		return nil
	}

	return cmd
}
