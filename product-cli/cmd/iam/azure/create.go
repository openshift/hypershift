package azure

import (
	"context"

	hypershiftazure "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// createIAMFunc creates the Azure IAM resources described by opts. It is a
// parameter of newCreateCommand so tests can exercise the command's control
// flow -- flag binding, validation ordering and error propagation -- without
// reaching Azure.
type createIAMFunc func(ctx context.Context, opts *hypershiftazure.CreateIAMOptions, l logr.Logger) error

// NewCreateCommand creates the Azure IAM create command for the product CLI
func NewCreateCommand() *cobra.Command {
	return newCreateCommand(func(ctx context.Context, opts *hypershiftazure.CreateIAMOptions, l logr.Logger) error {
		return opts.Run(ctx, l)
	})
}

func newCreateCommand(createIAM createIAMFunc) *cobra.Command {
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
		if err := createIAM(cmd.Context(), opts, l); err != nil {
			l.Error(err, "Failed to create IAM resources")
			return err
		}
		l.Info("Successfully created IAM resources")
		return nil
	}

	return cmd
}
