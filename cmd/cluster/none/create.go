package none

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
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

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
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
	if opts.NonePlatform.APIServerAddress == "" {
		if opts.NonePlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	infraID := opts.InfraID
	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID

	// TODO: We are inconsistent atm as DNS basedomain input is required and platform agnostic at the HostedCluster API level.
	// However In the CLI input is only exposed in AWS and not required atm. This should either be core input and required for
	// every platform or the field in the API should be optional if there's platform which don't need it.
	exampleOptions.BaseDomain = "example.com"

	exampleOptions.None = &apifixtures.ExampleNoneOptions{
		APIServerAddress: opts.NonePlatform.APIServerAddress,
	}
	return nil
}
