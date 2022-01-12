package none

import (
	"context"
	"fmt"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	nodepoolcore "github.com/openshift/hypershift/cmd/nodepool/core"
	nodepoolnone "github.com/openshift/hypershift/cmd/nodepool/none"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

type NonePlatformCreateOptions struct {
	APIServerAddress string
	NodePoolOptions  *nodepoolnone.NonePlatformCreateOptions
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Creates basic functional HostedCluster resources on None",
		SilenceUsage: true,
	}

	platformOpts := &NonePlatformCreateOptions{
		APIServerAddress: "",
	}

	cmd.Flags().StringVar(&platformOpts.APIServerAddress, "external-api-server-address", platformOpts.APIServerAddress, "The external API Server Address when using platform none")

	platformOpts.NodePoolOptions = nodepoolnone.NewNonePlatformCreateOptions(cmd)

	cmd.RunE = opts.CreateExecFunc(platformOpts)

	return cmd
}

func (o *NonePlatformCreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, name, infraID, baseDomain string) (err error) {
	if o.APIServerAddress == "" {
		if o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = baseDomain
	if exampleOptions.BaseDomain == "" {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.None = &apifixtures.ExampleNoneOptions{
		APIServerAddress: o.APIServerAddress,
	}
	return nil
}

func (o *NonePlatformCreateOptions) NodePoolPlatformOptions() nodepoolcore.PlatformOptions {
	return o.NodePoolOptions
}

func (o *NonePlatformCreateOptions) Validate() error {
	return o.NodePoolOptions.Validate()
}
