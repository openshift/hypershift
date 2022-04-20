package fixtures

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func ExampleKubeVirtTemplate(o *ExampleKubevirtOptions) *hyperv1.KubevirtNodePoolPlatform {
	runAlways := kubevirtv1.RunStrategyAlways
	guestQuantity := apiresource.MustParse(o.Memory)

	rootVolumeName := "rhcos"
	volumeSize := apiresource.MustParse(fmt.Sprintf("%vGi", o.RootVolumeSize))
	imageContainerURL := fmt.Sprintf("docker://%s", o.Image)

	exampleTemplate := &hyperv1.KubevirtNodePoolPlatform{
		NodeTemplate: &capikubevirt.VirtualMachineTemplateSpec{
			Spec: kubevirtv1.VirtualMachineSpec{
				RunStrategy: &runAlways,
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					Spec: kubevirtv1.VirtualMachineInstanceSpec{
						Domain: kubevirtv1.DomainSpec{
							CPU:    &kubevirtv1.CPU{Cores: o.Cores},
							Memory: &kubevirtv1.Memory{Guest: &guestQuantity},
							Devices: kubevirtv1.Devices{
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
	}
	exampleTemplate.NodeTemplate.Spec.Template.Spec.Domain.Devices.Disks = []kubevirtv1.Disk{
		{
			Name: rootVolumeName,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: "virtio",
				},
			},
		},
	}

	exampleTemplate.NodeTemplate.Spec.Template.Spec.Volumes = []kubevirtv1.Volume{
		{
			Name: rootVolumeName,
			VolumeSource: kubevirtv1.VolumeSource{
				DataVolume: &kubevirtv1.DataVolumeSource{
					Name: rootVolumeName,
				},
			},
		},
	}

	dataVolume := kubevirtv1.DataVolumeTemplateSpec{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: rootVolumeName,
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				Registry: &v1beta1.DataVolumeSourceRegistry{URL: &imageContainerURL},
			},
		},
	}

	dataVolume.Spec.Storage = &v1beta1.StorageSpec{
		Resources: corev1.ResourceRequirements{
			Requests: map[corev1.ResourceName]apiresource.Quantity{
				corev1.ResourceStorage: volumeSize,
			},
		},
	}

	if o.RootVolumeStorageClass != "" {
		dataVolume.Spec.Storage.StorageClassName = &o.RootVolumeStorageClass
	}

	exampleTemplate.NodeTemplate.Spec.DataVolumeTemplates = []kubevirtv1.DataVolumeTemplateSpec{dataVolume}

	return exampleTemplate
}
