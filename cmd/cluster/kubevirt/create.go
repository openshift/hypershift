package kubevirt

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

type CreateOptions struct {
	APIServerAddress   string
	Memory             string
	Cores              uint32
	ContainerDiskImage string
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources for KubeVirt platform",
		SilenceUsage: true,
	}

	platformOpts := CreateOptions{
		Memory:             "4Gi",
		Cores:              2,
		ContainerDiskImage: "",
	}

	cmd.Flags().StringVar(&platformOpts.Memory, "memory", platformOpts.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&platformOpts.Cores, "cores", platformOpts.Cores, "The number of cores inside the vmi, Must be a value greater or equal 1")
	cmd.Flags().StringVar(&platformOpts.ContainerDiskImage, "containerdisk", platformOpts.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")

	cmd.RunE = opts.CreateRunFunc(&platformOpts)

	return cmd
}

func (o *CreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if o.APIServerAddress == "" {
		if o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	if opts.NodePoolReplicas > -1 {
		// TODO (nargaman): replace with official container image, after RFE-2501 is completed
		// As long as there is no official container image
		// The image must be provided by user
		// Otherwise it must fail
		if o.ContainerDiskImage == "" {
			return errors.New("the container disk image for the Kubevirt machine must be provided by user (\"--containerdisk\" flag)")
		}
	}

	if o.Cores < 1 {
		return errors.New("the number of cores inside the machine must be a value greater or equal 1")
	}

	infraID := opts.InfraID
	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = "example.com"

	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		APIServerAddress: o.APIServerAddress,
		Memory:           o.Memory,
		Cores:            o.Cores,
		Image:            o.ContainerDiskImage,
	}
	return nil
}
