package none

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

type CreateOptions struct {
	APIServerAddress string
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Creates basic functional HostedCluster resources on None",
		SilenceUsage: true,
	}

	platformOpts := CreateOptions{
		APIServerAddress: "",
	}

	cmd.Flags().StringVar(&platformOpts.APIServerAddress, "external-api-server-address", platformOpts.APIServerAddress, "The external API Server Address when using platform none")

	cmd.RunE = opts.CreateRunFunc(&platformOpts)

	return cmd
}

func (o *CreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if o.APIServerAddress == "" {
		if o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	infraID := opts.InfraID
	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = opts.BaseDomain
	if exampleOptions.BaseDomain == "" {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.None = &apifixtures.ExampleNoneOptions{
		APIServerAddress: o.APIServerAddress,
	}
	return nil
}
