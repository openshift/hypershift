package kubevirt

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	apiresource "k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

const (
	NodePortServicePublishingStrategy = "NodePort"
	IngressServicePublishingStrategy  = "Ingress"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources for KubeVirt platform",
		SilenceUsage: true,
	}

	opts.KubevirtPlatform = core.KubevirtPlatformCreateOptions{
		ServicePublishingStrategy: IngressServicePublishingStrategy,
		Memory:                    "4Gi",
		Cores:                     2,
		ContainerDiskImage:        "",
	}

	cmd.Flags().StringVar(&opts.KubevirtPlatform.Memory, "memory", opts.KubevirtPlatform.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.Cores, "cores", opts.KubevirtPlatform.Cores, "The number of cores inside the vmi, Must be a value greater or equal 1")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ContainerDiskImage, "containerdisk", opts.KubevirtPlatform.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ServicePublishingStrategy, "service-publishing-strategy", opts.KubevirtPlatform.ServicePublishingStrategy, fmt.Sprintf("Define how to expose the cluster services. Supported options: %s (Use LoadBalancer and Route to expose services), %s (Select a random node to expose service access through)", IngressServicePublishingStrategy, NodePortServicePublishingStrategy))

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
	if opts.KubevirtPlatform.ServicePublishingStrategy != NodePortServicePublishingStrategy && opts.KubevirtPlatform.ServicePublishingStrategy != IngressServicePublishingStrategy {
		return fmt.Errorf("service publish strategy %s is not supported, supported options: %s, %s", opts.KubevirtPlatform.ServicePublishingStrategy, IngressServicePublishingStrategy, NodePortServicePublishingStrategy)
	}
	if opts.KubevirtPlatform.ServicePublishingStrategy != NodePortServicePublishingStrategy && opts.KubevirtPlatform.APIServerAddress != "" {
		return fmt.Errorf("external-api-server-address is supported only for NodePort service publishing strategy, service publishing strategy %s is used", opts.KubevirtPlatform.ServicePublishingStrategy)
	}
	if opts.KubevirtPlatform.APIServerAddress == "" && opts.KubevirtPlatform.ServicePublishingStrategy == NodePortServicePublishingStrategy && !opts.Render {
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

	if opts.BaseDomain != "" {
		exampleOptions.BaseDomain = opts.BaseDomain
	} else {
		exampleOptions.BaseDomain = "example.com"
	}

	runAlways := kubevirtv1.RunStrategyAlways
	guestQuantity := apiresource.MustParse(opts.KubevirtPlatform.Memory)
	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		ServicePublishingStrategy: opts.KubevirtPlatform.ServicePublishingStrategy,
		APIServerAddress:          opts.KubevirtPlatform.APIServerAddress,
		Memory:                    opts.KubevirtPlatform.Memory,
		Cores:                     opts.KubevirtPlatform.Cores,
		Image:                     opts.KubevirtPlatform.ContainerDiskImage,
		NodeTemplate: func() *runtime.RawExtension {
			return &runtime.RawExtension{
				Object: &capikubevirt.KubevirtMachine{
					Spec: capikubevirt.KubevirtMachineSpec{
						VirtualMachineTemplate: capikubevirt.VirtualMachineTemplateSpec{
							Spec: kubevirtv1.VirtualMachineSpec{
								RunStrategy: &runAlways,
								Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
									Spec: kubevirtv1.VirtualMachineInstanceSpec{
										Domain: kubevirtv1.DomainSpec{
											CPU:    &kubevirtv1.CPU{Cores: opts.KubevirtPlatform.Cores},
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
													{
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
														Image: opts.KubevirtPlatform.ContainerDiskImage,
													},
												},
											},
										},
										Networks: []kubevirtv1.Network{
											{
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
					},
				},
			}
		},
	}

	return nil
}
