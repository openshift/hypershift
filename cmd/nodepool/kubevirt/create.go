package kubevirt

import (
	"context"

	"github.com/spf13/cobra"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
)

type KubevirtPlatformCreateOptions struct {
	Memory             string
	Cores              uint32
	ContainerDiskImage string
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
	cmd.Flags().StringVar(&platformOpts.ContainerDiskImage, "containerdisk", platformOpts.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")

	// TODO (nargaman): replace with official container image, after RFE-2501 is completed
	// As long as there is no official container image
	// The image must be provided by user
	// Otherwise it must fail
	cmd.MarkFlagRequired("containerdisk")

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *KubevirtPlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	runAlways := kubevirtv1.RunStrategyAlways
	guestQuantity := apiresource.MustParse(o.Memory)
	nodePool.Spec.Platform.Kubevirt = &hyperv1.KubevirtNodePoolPlatform{
		NodeTemplate: &capikubevirt.VirtualMachineTemplateSpec{
			Spec: kubevirtv1.VirtualMachineSpec{
				RunStrategy: &runAlways,
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					Spec: kubevirtv1.VirtualMachineInstanceSpec{
						Domain: kubevirtv1.DomainSpec{
							CPU:    &kubevirtv1.CPU{Cores: o.Cores},
							Memory: &kubevirtv1.Memory{Guest: &guestQuantity},
							Devices: kubevirtv1.Devices{
								Disks: []kubevirtv1.Disk{
									{
										Name: "containervolume",
										DiskDevice: kubevirtv1.DiskDevice{
											Disk: &kubevirtv1.DiskTarget{
												Bus: "virtio",
											},
										},
									},
								},
								Interfaces: []kubevirtv1.Interface{
									kubevirtv1.Interface{
										Name: "default",
										InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
											Bridge: &kubevirtv1.InterfaceBridge{},
										},
									},
								},
							},
						},
						Volumes: []kubevirtv1.Volume{
							{
								Name: "containervolume",
								VolumeSource: kubevirtv1.VolumeSource{
									ContainerDisk: &kubevirtv1.ContainerDiskSource{
										Image: o.ContainerDiskImage,
									},
								},
							},
						},
						Networks: []kubevirtv1.Network{
							kubevirtv1.Network{
								Name: "default",
								NetworkSource: kubevirtv1.NetworkSource{
									Pod: &kubevirtv1.PodNetwork{},
								},
							},
						},
					},
				},
			},
		},
	}
	return nil
}

func (o *KubevirtPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.KubevirtPlatform
}
