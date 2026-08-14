package azure

import (
	"context"

	hypershiftazure "github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/config"

	"github.com/spf13/cobra"
)

func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage: true,
	}

	opts.ReleaseStream = config.DefaultReleaseStream

	azureOpts := hypershiftazure.DefaultOptions()
	hypershiftazure.BindProductFlags(azureOpts, cmd.Flags())
	hypershiftazure.BindProductCoreFlags(opts, cmd.Flags())

	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		return core.CreateCluster(ctx, opts, azureOpts)
	}

	return cmd
}
