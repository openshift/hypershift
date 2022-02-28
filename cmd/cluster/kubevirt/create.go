package kubevirt

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources for KubeVirt platform",
		SilenceUsage: true,
	}

	opts.KubevirtPlatform = core.KubevirtPlatformCreateOptions{
		Memory:             "4Gi",
		Cores:              2,
		ContainerDiskImage: "",
	}

	cmd.Flags().StringVar(&opts.KubevirtPlatform.Memory, "memory", opts.KubevirtPlatform.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.Cores, "cores", opts.KubevirtPlatform.Cores, "The number of cores inside the vmi, Must be a value greater or equal 1")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ContainerDiskImage, "containerdisk", opts.KubevirtPlatform.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts); err != nil {
			log.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if opts.KubevirtPlatform.APIServerAddress == "" {
		if opts.KubevirtPlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	if opts.NodePoolReplicas > -1 {
		// TODO (nargaman): replace with official container image, after RFE-2501 is completed
		// As long as there is no official container image
		// The image must be provided by user
		// Otherwise it must fail
		if opts.KubevirtPlatform.ContainerDiskImage == "" {
			return errors.New("the container disk image for the Kubevirt machine must be provided by user (\"--containerdisk\" flag)")
		}
	}

	if opts.KubevirtPlatform.Cores < 1 {
		return errors.New("the number of cores inside the machine must be a value greater or equal 1")
	}

	infraID := opts.InfraID
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = "example.com"

	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		APIServerAddress: opts.KubevirtPlatform.APIServerAddress,
		Memory:           opts.KubevirtPlatform.Memory,
		Cores:            opts.KubevirtPlatform.Cores,
		Image:            opts.KubevirtPlatform.ContainerDiskImage,
	}
	return nil
}
