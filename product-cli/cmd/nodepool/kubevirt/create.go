package kubevirt

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/nodepool/core"
	hyperShiftKubeVirt "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := &hyperShiftKubeVirt.KubevirtPlatformCreateOptions{
		Memory:             "4Gi",
		Cores:              2,
		ContainerDiskImage: "",
		CacheStrategyType:  "",
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
	cmd.Flags().StringVar(&platformOpts.CacheStrategyType, "root-volume-cache-strategy", platformOpts.CacheStrategyType, "Set the boot image caching strategy; Supported values:\n- \"None\": no caching (default).\n- \"PVC\": Cache into a PVC; only for QCOW image; ignored for container images")

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}
