package kubevirt

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/api/fixtures"
	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
)

type KubevirtPlatformCreateOptions struct {
	Memory                     string
	Cores                      uint32
	ContainerDiskImage         string
	RootVolumeSize             uint32
	RootVolumeStorageClass     string
	RootVolumeAccessModes      string
	RootVolumeVolumeMode       string
	CacheStrategyType          string
	NetworkInterfaceMultiQueue string
	QoSClass                   string
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := &KubevirtPlatformCreateOptions{
		Memory:                     "4Gi",
		Cores:                      2,
		ContainerDiskImage:         "",
		RootVolumeSize:             32,
		CacheStrategyType:          "",
		NetworkInterfaceMultiQueue: "",
		QoSClass:                   "Burstable",
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
	cmd.Flags().StringVar(&platformOpts.RootVolumeVolumeMode, "root-volume-volume-mode", platformOpts.RootVolumeVolumeMode, "The volume mode of the root volume to use for machines in the NodePool. Supported values are \"Block\", \"Filesystem\"")
	cmd.Flags().StringVar(&platformOpts.ContainerDiskImage, "containerdisk", platformOpts.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")
	cmd.Flags().StringVar(&platformOpts.CacheStrategyType, "root-volume-cache-strategy", platformOpts.CacheStrategyType, "Set the boot image caching strategy; Supported values:\n- \"None\": no caching (default).\n- \"PVC\": Cache into a PVC; only for QCOW image; ignored for container images")
	cmd.Flags().StringVar(&platformOpts.NetworkInterfaceMultiQueue, "network-multiqueue", platformOpts.NetworkInterfaceMultiQueue, `If "Enable", virtual network interfaces configured with a virtio bus will also enable the vhost multiqueue feature for network devices. supported values are "Enable" and "Disable"; default = "Disable"`)
	cmd.Flags().StringVar(&platformOpts.QoSClass, "qos-class", platformOpts.QoSClass, `If "Guaranteed", set the limit cpu and memory of the VirtualMachineInstance, to be the same as the requested cpu and memory; supported values: "Burstable" and "Guaranteed"`)

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *KubevirtPlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	if o.CacheStrategyType != "" &&
		o.CacheStrategyType != string(hyperv1.KubevirtCachingStrategyNone) &&
		o.CacheStrategyType != string(hyperv1.KubevirtCachingStrategyPVC) {
		return fmt.Errorf(`wrong value for the --root-volume-cache-strategy parameter. May be only "None" or "PVC"`)
	}

	var multiQueue *hyperv1.MultiQueueSetting
	switch value := hyperv1.MultiQueueSetting(o.NetworkInterfaceMultiQueue); value {
	case "": // do nothing; value is nil
	case hyperv1.MultiQueueEnable, hyperv1.MultiQueueDisable:
		multiQueue = &value
	default:
		return fmt.Errorf(`wrong value for the --network-multiqueue parameter. Supported values are "Enable" or "Disable"`)
	}

	var qosClass *hyperv1.QoSClass
	switch value := hyperv1.QoSClass(o.QoSClass); value {
	case "": // do nothing; value is nil
	case hyperv1.QoSClassBurstable, hyperv1.QoSClassGuaranteed:
		qosClass = &value
	default:
		return fmt.Errorf(`wrong value for the --qos-class parameter. Supported values are "Burstable" are "Guaranteed"`)
	}

	nodePool.Spec.Platform.Kubevirt = fixtures.ExampleKubeVirtTemplate(&fixtures.ExampleKubevirtOptions{
		Memory:                     o.Memory,
		Cores:                      o.Cores,
		Image:                      o.ContainerDiskImage,
		RootVolumeSize:             o.RootVolumeSize,
		RootVolumeStorageClass:     o.RootVolumeStorageClass,
		RootVolumeAccessModes:      o.RootVolumeAccessModes,
		RootVolumeVolumeMode:       o.RootVolumeVolumeMode,
		CacheStrategyType:          o.CacheStrategyType,
		NetworkInterfaceMultiQueue: multiQueue,
		QoSClass:                   qosClass,
	})
	return nil
}

func (o *KubevirtPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.KubevirtPlatform
}
