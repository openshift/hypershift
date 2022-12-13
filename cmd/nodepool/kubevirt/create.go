package kubevirt

import (
	"context"

	"github.com/openshift/hypershift/api/fixtures"
	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
)

type KubevirtPlatformCreateOptions struct {
	Memory                 string
	Cores                  uint32
	ContainerDiskImage     string
	RootVolumeSize         uint32
	RootVolumeStorageClass string
	RootVolumeAccessModes  string
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := &KubevirtPlatformCreateOptions{
		Memory:             "4Gi",
		Cores:              2,
		ContainerDiskImage: "",
	}
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional NodePool resources for KubeVirt platform",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&platformOpts.Memory, "memory", platformOpts.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&platformOpts.Cores, "cores", platformOpts.Cores, "The number of cores inside the vmi, Must be a value greater or equal 1")
	cmd.Flags().StringVar(&platformOpts.RootVolumeStorageClass, "root-volume-storage-class", platformOpts.RootVolumeStorageClass, "The storage class to use for machines in the NodePool")
	cmd.Flags().Uint32Var(&platformOpts.RootVolumeSize, "root-volume-size", platformOpts.RootVolumeSize, "The size of the root volume for machines in the NodePool in Gi")
	cmd.Flags().StringVar(&platformOpts.RootVolumeAccessModes, "root-volume-access-modes", platformOpts.RootVolumeAccessModes, "The access modes of the root volume to use for machines in the NodePool (comma-delimited list)")
	cmd.Flags().StringVar(&platformOpts.ContainerDiskImage, "containerdisk", platformOpts.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *KubevirtPlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	nodePool.Spec.Platform.Kubevirt = fixtures.ExampleKubeVirtTemplate(&fixtures.ExampleKubevirtOptions{
		Memory:                 o.Memory,
		Cores:                  o.Cores,
		Image:                  o.ContainerDiskImage,
		RootVolumeSize:         o.RootVolumeSize,
		RootVolumeStorageClass: o.RootVolumeStorageClass,
		RootVolumeAccessModes:  o.RootVolumeAccessModes,
	})
	return nil
}

func (o *KubevirtPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.KubevirtPlatform
}
