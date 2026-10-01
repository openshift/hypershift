package azure

import (
	"context"

	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// destroyInfraFunc destroys the Azure infrastructure described by opts. It is a
// parameter of newDestroyCommand so tests can exercise the command's control
// flow -- flag binding, validation ordering and error propagation -- without
// reaching Azure.
type destroyInfraFunc func(ctx context.Context, opts *hypershiftazure.DestroyInfraOptions, l logr.Logger) error

// NewDestroyCommand creates the Azure infrastructure destroy command for the product CLI
func NewDestroyCommand() *cobra.Command {
	return newDestroyCommand(func(ctx context.Context, opts *hypershiftazure.DestroyInfraOptions, l logr.Logger) error {
		return opts.Run(ctx, l)
	})
}

func newDestroyCommand(destroyInfra destroyInfraFunc) *cobra.Command {
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

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := destroyInfra(cmd.Context(), opts, l); err != nil {
			l.Error(err, "Failed to destroy infrastructure")
			return err
		}
		l.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}
