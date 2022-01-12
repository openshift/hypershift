package kubevirt

import (
	"context"
	"fmt"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	nodepoolcore "github.com/openshift/hypershift/cmd/nodepool/core"
	nodepoolkubevirt "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

type KubevirtPlatformCreateOptions struct {
	APIServerAddress string
	NodePoolOptions  *nodepoolkubevirt.KubevirtPlatformCreateOptions
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources for KubeVirt platform",
		SilenceUsage: true,
	}

	platformOpts := &KubevirtPlatformCreateOptions{}

	platformOpts.NodePoolOptions = nodepoolkubevirt.NewKubevirtPlatformCreateOptions(cmd)

	cmd.RunE = opts.CreateExecFunc(platformOpts)

	return cmd
}

func (o *KubevirtPlatformCreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, name, infraID, baseDomain string) (err error) {
	if o.APIServerAddress == "" {
		if o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = "example.com"

	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		APIServerAddress: o.APIServerAddress,
	}
	return nil
}

func (o *KubevirtPlatformCreateOptions) NodePoolPlatformOptions() nodepoolcore.PlatformOptions {
	return o.NodePoolOptions
}

func (o *KubevirtPlatformCreateOptions) Validate() error {
	return o.NodePoolOptions.Validate()
}
