package kubevirt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openshift/hypershift/cmd/cluster/kubevirt/params"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
)

func DefaultOptions() *KubevirtPlatformCreateOptions {
	return &KubevirtPlatformCreateOptions{
		KubevirtPlatformOptions: KubevirtPlatformOptions{
			Memory:               "4Gi",
			Cores:                2,
			RootVolumeSize:       32,
			AttachDefaultNetwork: pointer.Bool(true),
		},
		QoSClass: "Burstable",
	}
}

func BindOptions(opts *KubevirtPlatformCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
}

func bindCoreOptions(opts *KubevirtPlatformCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.Memory, "memory", opts.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	flags.Uint32Var(&opts.Cores, "cores", opts.Cores, "The number of cores inside the vmi, Must be a value greater or equal 1")
	flags.StringVar(&opts.RootVolumeStorageClass, "root-volume-storage-class", opts.RootVolumeStorageClass, "The storage class to use for machines in the NodePool")
	flags.Uint32Var(&opts.RootVolumeSize, "root-volume-size", opts.RootVolumeSize, "The size of the root volume for machines in the NodePool in Gi")
	flags.StringVar(&opts.RootVolumeAccessModes, "root-volume-access-modes", opts.RootVolumeAccessModes, "The access modes of the root volume to use for machines in the NodePool (comma-delimited list)")
	flags.StringVar(&opts.RootVolumeVolumeMode, "root-volume-volume-mode", opts.RootVolumeVolumeMode, "The volume mode of the root volume to use for machines in the NodePool. Supported values are \"Block\", \"Filesystem\"")
	flags.StringVar(&opts.CacheStrategyType, "root-volume-cache-strategy", opts.CacheStrategyType, "Set the boot image caching strategy; Supported values:\n- \"None\": no caching (default).\n- \"PVC\": Cache into a PVC; only for QCOW image; ignored for container images")
	flags.StringVar(&opts.NetworkInterfaceMultiQueue, "network-multiqueue", opts.NetworkInterfaceMultiQueue, `If "Enable", virtual network interfaces configured with a virtio bus will also enable the vhost multiqueue feature for network devices. supported values are "Enable" and "Disable"; default = "Disable"`)
	flags.StringVar(&opts.QoSClass, "qos-class", opts.QoSClass, `If "Guaranteed", set the limit cpu and memory of the VirtualMachineInstance, to be the same as the requested cpu and memory; supported values: "Burstable" and "Guaranteed"`)
	flags.StringArrayVar(&opts.AdditionalNetworks, "additional-network", opts.AdditionalNetworks, fmt.Sprintf(`Specify additional network that should be attached to the nodes, the "name" field should point to a multus network attachment definition with the format "[namespace]/[name]", it can be specified multiple times to attach to multiple networks. Supported parameters: %s, example: "name:ns1/nad-foo`, params.Supported(NetworkOpts{})))
	flags.BoolVar(opts.AttachDefaultNetwork, "attach-default-network", *opts.AttachDefaultNetwork, `Specify if the default pod network should be attached to the nodes. This can only be set if --additional-network is configured`)
	flags.StringToStringVar(&opts.VmNodeSelector, "vm-node-selector", opts.VmNodeSelector, "A comma separated list of key=value pairs to use as the node selector for the KubeVirt VirtualMachines to be scheduled onto. (e.g. role=kubevirt,size=large)")
}

func BindDeveloperOptions(opts *KubevirtPlatformCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
	flags.StringVar(&opts.ContainerDiskImage, "containerdisk", opts.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")
}

type KubevirtPlatformCreateOptions struct {
	KubevirtPlatformOptions
	NetworkInterfaceMultiQueue string
	QoSClass                   string
	AdditionalNetworks         []string
}

type KubevirtPlatformOptions struct {
	Memory                 string
	Cores                  uint32
	ContainerDiskImage     string
	RootVolumeSize         uint32
	RootVolumeStorageClass string
	RootVolumeAccessModes  string
	RootVolumeVolumeMode   string
	CacheStrategyType      string

	AttachDefaultNetwork *bool
	VmNodeSelector       map[string]string
}

type KubevirtPlatformCompletedOptions struct {
	KubevirtPlatformOptions
	MultiQueue         *hyperv1.MultiQueueSetting
	QoSClass           *hyperv1.QoSClass
	AdditionalNetworks []hyperv1.KubevirtNetwork
}

func (o *KubevirtPlatformCreateOptions) Validate() error {
	if o.CacheStrategyType != "" &&
		o.CacheStrategyType != string(hyperv1.KubevirtCachingStrategyNone) &&
		o.CacheStrategyType != string(hyperv1.KubevirtCachingStrategyPVC) {
		return fmt.Errorf(`wrong value for the --root-volume-cache-strategy parameter. May be only "None" or "PVC"`)
	}

	if o.RootVolumeVolumeMode != "" &&
		o.RootVolumeVolumeMode != string(corev1.PersistentVolumeBlock) &&
		o.RootVolumeVolumeMode != string(corev1.PersistentVolumeFilesystem) {

		return fmt.Errorf(`unsupported value for the --root-volume-volume-mode parameter. May be only "Filesystem" or "Block"`)
	}

	if o.Cores < 1 {
		return errors.New("the number of cores inside the machine must be a value greater than or equal to 1")
	}

	if o.RootVolumeSize < 16 {
		return fmt.Errorf("the root volume size [%d] must be greater than or equal to 16", o.RootVolumeSize)
	}

	if len(o.AdditionalNetworks) == 0 && o.AttachDefaultNetwork != nil && !*o.AttachDefaultNetwork {
		return fmt.Errorf(`missing --additional-network. when --attach-default-network is false configuring an additional network is mandatory`)
	}
	return nil
}

type NetworkOpts struct {
	Name string `param:"name"`
}

func (o *KubevirtPlatformCreateOptions) Complete() (*KubevirtPlatformCompletedOptions, error) {
	var multiQueue *hyperv1.MultiQueueSetting
	switch value := hyperv1.MultiQueueSetting(o.NetworkInterfaceMultiQueue); value {
	case "": // do nothing; value is nil
	case hyperv1.MultiQueueEnable, hyperv1.MultiQueueDisable:
		multiQueue = &value
	default:
		return nil, fmt.Errorf(`wrong value for the --network-multiqueue parameter. Supported values are "Enable" or "Disable"`)
	}

	var qosClass *hyperv1.QoSClass
	switch value := hyperv1.QoSClass(o.QoSClass); value {
	case "": // do nothing; value is nil
	case hyperv1.QoSClassBurstable, hyperv1.QoSClassGuaranteed:
		qosClass = &value
	default:
		return nil, fmt.Errorf(`wrong value for the --qos-class parameter. Supported values are "Burstable" are "Guaranteed"`)
	}

	var additionalNetworks []hyperv1.KubevirtNetwork
	for _, additionalNetworkOptsRaw := range o.AdditionalNetworks {
		additionalNetworkOpts := NetworkOpts{}
		if err := params.Map("additional-network", additionalNetworkOptsRaw, &additionalNetworkOpts); err != nil {
			return nil, err
		}
		additionalNetworks = append(additionalNetworks, hyperv1.KubevirtNetwork{
			Name: additionalNetworkOpts.Name,
		})
	}

	return &KubevirtPlatformCompletedOptions{
		KubevirtPlatformOptions: o.KubevirtPlatformOptions,
		MultiQueue:              multiQueue,
		QoSClass:                qosClass,
		AdditionalNetworks:      additionalNetworks,
	}, nil
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := DefaultOptions()
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional NodePool resources for KubeVirt platform",
		SilenceUsage: true,
	}
	BindDeveloperOptions(platformOpts, cmd.Flags())
	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *KubevirtPlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	// TODO: it's likely good to refactor the top-level node pool CLI to expect every platform to follow this pattern
	if err := o.Validate(); err != nil {
		return err
	}
	completed, err := o.Complete()
	if err != nil {
		return err
	}

	nodePool.Spec.Platform.Kubevirt = completed.NodePoolPlatform()
	return nil
}

func (o *KubevirtPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.KubevirtPlatform
}

func (o *KubevirtPlatformCompletedOptions) NodePoolPlatform() *hyperv1.KubevirtNodePoolPlatform {
	var storageClassName *string
	var accessModesStr []string
	var accessModes []hyperv1.PersistentVolumeAccessMode
	volumeSize := apiresource.MustParse(fmt.Sprintf("%vGi", o.RootVolumeSize))

	if o.RootVolumeStorageClass != "" {
		storageClassName = &o.RootVolumeStorageClass
	}

	if o.RootVolumeAccessModes != "" {
		accessModesStr = strings.Split(o.RootVolumeAccessModes, ",")
		for _, ams := range accessModesStr {
			accessModes = append(accessModes, hyperv1.PersistentVolumeAccessMode(ams))
		}
	}

	platform := &hyperv1.KubevirtNodePoolPlatform{
		RootVolume: &hyperv1.KubevirtRootVolume{
			KubevirtVolume: hyperv1.KubevirtVolume{
				Type: hyperv1.KubevirtVolumeTypePersistent,
				Persistent: &hyperv1.KubevirtPersistentVolume{
					Size:         &volumeSize,
					StorageClass: storageClassName,
					AccessModes:  accessModes,
				},
			},
		},
		Compute:              &hyperv1.KubevirtCompute{},
		AdditionalNetworks:   o.AdditionalNetworks,
		AttachDefaultNetwork: o.AttachDefaultNetwork,
	}

	if o.RootVolumeVolumeMode != "" {
		vm := corev1.PersistentVolumeMode(o.RootVolumeVolumeMode)
		platform.RootVolume.KubevirtVolume.Persistent.VolumeMode = &vm
	}

	if o.Memory != "" {
		memory := apiresource.MustParse(o.Memory)
		platform.Compute.Memory = &memory
	}
	if o.Cores != 0 {
		platform.Compute.Cores = &o.Cores
	}

	if o.QoSClass != nil && *o.QoSClass == hyperv1.QoSClassGuaranteed {
		platform.Compute.QosClass = o.QoSClass
	}

	if o.ContainerDiskImage != "" {
		platform.RootVolume.Image = &hyperv1.KubevirtDiskImage{
			ContainerDiskImage: &o.ContainerDiskImage,
		}
	}

	strategyType := hyperv1.KubevirtCachingStrategyType(o.CacheStrategyType)
	if strategyType == hyperv1.KubevirtCachingStrategyNone || strategyType == hyperv1.KubevirtCachingStrategyPVC {
		platform.RootVolume.CacheStrategy = &hyperv1.KubevirtCachingStrategy{
			Type: strategyType,
		}
	}

	if o.MultiQueue != nil && *o.MultiQueue == hyperv1.MultiQueueEnable {
		platform.NetworkInterfaceMultiQueue = o.MultiQueue
	}

	if o.VmNodeSelector != nil && len(o.VmNodeSelector) > 0 {
		platform.NodeSelector = o.VmNodeSelector
	}
	return platform
}
