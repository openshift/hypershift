package kubevirt

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt/params"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources on KubeVirt platform",
		SilenceUsage: true,
	}

	opts.KubevirtPlatform = core.KubevirtPlatformCreateOptions{
		ServicePublishingStrategy:  kubevirt.IngressServicePublishingStrategy,
		APIServerAddress:           "",
		Memory:                     "8Gi",
		Cores:                      2,
		ContainerDiskImage:         "",
		RootVolumeSize:             32,
		InfraKubeConfigFile:        "",
		CacheStrategyType:          "",
		NetworkInterfaceMultiQueue: "",
		QoSClass:                   "Burstable",
		AttachDefaultNetwork:       pointer.Bool(true),
	}

	cmd.Flags().StringVar(&opts.KubevirtPlatform.Memory, "memory", opts.KubevirtPlatform.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.Cores, "cores", opts.KubevirtPlatform.Cores, "The number of cores inside the vmi, Must be a value greater than or equal to 1")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeStorageClass, "root-volume-storage-class", opts.KubevirtPlatform.RootVolumeStorageClass, "The storage class to use for machines in the NodePool")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.RootVolumeSize, "root-volume-size", opts.KubevirtPlatform.RootVolumeSize, "The size of the root volume for machines in the NodePool in Gi")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeAccessModes, "root-volume-access-modes", opts.KubevirtPlatform.RootVolumeAccessModes, "The access modes of the root volume to use for machines in the NodePool (comma-delimited list)")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeVolumeMode, "root-volume-volume-mode", opts.KubevirtPlatform.RootVolumeVolumeMode, "The volume mode of the root volume to use for machines in the NodePool. Supported values are \"Block\", \"Filesystem\"")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.InfraKubeConfigFile, "infra-kubeconfig-file", opts.KubevirtPlatform.InfraKubeConfigFile, "Path to a kubeconfig file of an external infra cluster to be used to create the guest clusters nodes onto")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.InfraNamespace, "infra-namespace", opts.KubevirtPlatform.InfraNamespace, "The namespace in the external infra cluster that is used to host the KubeVirt virtual machines. The namespace must exist prior to creating the HostedCluster")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.CacheStrategyType, "root-volume-cache-strategy", opts.KubevirtPlatform.CacheStrategyType, "Set the boot image caching strategy; Supported values:\n- \"None\": no caching (default).\n- \"PVC\": Cache into a PVC; only for QCOW image; ignored for container images")
	cmd.Flags().StringArrayVar(&opts.KubevirtPlatform.InfraStorageClassMappings, "infra-storage-class-mapping", opts.KubevirtPlatform.InfraStorageClassMappings, "KubeVirt CSI napping of an infra StorageClass to a guest cluster StorageCluster. Mapping is structured as <infra storage class>/<guest storage class>. Example, mapping the infra storage class ocs-storagecluster-ceph-rbd to a guest storage class called ceph-rdb. --infra-storage-class-mapping=ocs-storagecluster-ceph-rbd/ceph-rdb")
	cmd.Flags().StringArrayVar(&opts.KubevirtPlatform.InfraVolumeSnapshotClassMappings, "infra-volumesnapshot-class-mapping", opts.KubevirtPlatform.InfraVolumeSnapshotClassMappings, "KubeVirt CSI mapping of an infra VolumeSnapshotClass to a guest cluster VolumeSnapshotCluster. Mapping is structured as <infra volume snapshot class>/<guest volume snapshot class>. Example, mapping the infra volume snapshot class ocs-storagecluster-rbd-snap to a guest volume snapshot class called rdb-snap. --infra-volumesnapshot-class-mapping=ocs-storagecluster-rbd-snap/rdb-snap. Group storage classes and volumesnapshot classes by adding ,group=<group name>")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.NetworkInterfaceMultiQueue, "network-multiqueue", opts.KubevirtPlatform.NetworkInterfaceMultiQueue, `If "Enable", virtual network interfaces configured with a virtio bus will also enable the vhost multiqueue feature for network devices. supported values are "Enable" and "Disable"; default = "Disable"`)
	cmd.Flags().StringVar(&opts.KubevirtPlatform.QoSClass, "qos-class", opts.KubevirtPlatform.QoSClass, `If "Guaranteed", set the limit cpu and memory of the VirtualMachineInstance, to be the same as the requested cpu and memory; supported values: "Burstable" and "Guaranteed"`)
	cmd.Flags().StringArrayVar(&opts.KubevirtPlatform.AdditionalNetworks, "additional-network", opts.KubevirtPlatform.AdditionalNetworks, fmt.Sprintf(`Specify additional network that should be attached to the nodes, the "name" field should point to a multus network attachment definition with the format "[namespace]/[name]", it can be specified multiple times to attach to multiple networks. Supported parameters: %s, example: "name:ns1/nad-foo`, params.Supported(kubevirt.NetworkOpts{})))
	cmd.Flags().BoolVar(opts.KubevirtPlatform.AttachDefaultNetwork, "attach-default-network", *opts.KubevirtPlatform.AttachDefaultNetwork, `Specify if the default pod network should be attached to the nodes. This can only be set if --addtional-network is configured`)
	cmd.Flags().StringToStringVar(&opts.KubevirtPlatform.VmNodeSelector, "vm-node-selector", opts.KubevirtPlatform.VmNodeSelector, "A comma separated list of key=value pairs to use as the node selector for the KubeVirt VirtualMachines to be scheduled onto. (e.g. role=kubevirt,size=large)")

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
	return core.CreateCluster(ctx, opts, kubevirt.ApplyPlatformSpecificsValues)
}
