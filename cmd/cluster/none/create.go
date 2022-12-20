package none

import (
	"context"

	"github.com/spf13/cobra"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Creates basic functional HostedCluster resources on None",
		SilenceUsage: true,
	}

	opts.NonePlatform = core.NonePlatformCreateOptions{
		APIServerAddress: "",
	}

	cmd.Flags().StringVar(&opts.NonePlatform.APIServerAddress, "external-api-server-address", opts.NonePlatform.APIServerAddress, "The external API Server Address when using platform none")
	cmd.Flags().BoolVar(&opts.NonePlatform.ExposeThroughLoadBalancer, "expose-through-load-balancer", opts.NonePlatform.ExposeThroughLoadBalancer, "If the services should be exposed through LoadBalancer. If not set, nodeports will be used instead")

	cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	if err := core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if opts.NonePlatform.APIServerAddress == "" && !opts.NonePlatform.ExposeThroughLoadBalancer {
		if opts.NonePlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log); err != nil {
			return err
		}
	}

	infraID := opts.InfraID
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = opts.BaseDomain
	if exampleOptions.BaseDomain == "" {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.None = &apifixtures.ExampleNoneOptions{
		APIServerAddress: opts.NonePlatform.APIServerAddress,
	}
	return nil
}
