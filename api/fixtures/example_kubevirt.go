package fixtures

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/util"
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
			Name: util.RHCOSMagicVolumeName,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: "virtio",
				},
			},
		},
	}
	if o.Image != "" {
		exampleTemplate.NodeTemplate.Spec.Template.Spec.Volumes = []kubevirtv1.Volume{
			{
				Name: util.RHCOSMagicVolumeName,
				VolumeSource: kubevirtv1.VolumeSource{
					ContainerDisk: &kubevirtv1.ContainerDiskSource{
						Image: o.Image,
					},
				},
			},
		}
	} else {
		dataVolume := defaultDataVolume()
		setDataVolumeDefaults(&dataVolume, o)
		exampleTemplate.NodeTemplate.Spec.DataVolumeTemplates = []kubevirtv1.DataVolumeTemplateSpec{dataVolume}
		exampleTemplate.NodeTemplate.Spec.Template.Spec.Volumes = []kubevirtv1.Volume{
			{
				Name: util.RHCOSMagicVolumeName,
				VolumeSource: kubevirtv1.VolumeSource{
					DataVolume: &kubevirtv1.DataVolumeSource{
						Name: util.RHCOSMagicVolumeName,
					},
				},
			},
		}
	}
	return exampleTemplate
}

func defaultDataVolume() kubevirtv1.DataVolumeTemplateSpec {
	return kubevirtv1.DataVolumeTemplateSpec{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: util.RHCOSMagicVolumeName,
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				HTTP: &v1beta1.DataVolumeSourceHTTP{URL: util.RHCOSOpenStackURLParam},
			},
		},
	}
}
func setDataVolumeDefaults(spec *kubevirtv1.DataVolumeTemplateSpec, o *ExampleKubevirtOptions) {
	if spec.Spec.Storage == nil {
		spec.Spec.Storage = &v1beta1.StorageSpec{
			Resources: corev1.ResourceRequirements{
				Requests: map[corev1.ResourceName]apiresource.Quantity{
					corev1.ResourceStorage: util.KubeVirtVolumeDefaultSize,
				},
			},
			StorageClassName: &o.RootVolumeStorageClass,
		}
	}
	if o.RootVolumeSize != 0 {
		size := apiresource.MustParse(fmt.Sprintf("%vGi", o.RootVolumeSize))
		spec.Spec.Storage.Resources = corev1.ResourceRequirements{
			Requests: map[corev1.ResourceName]apiresource.Quantity{
				corev1.ResourceStorage: size,
			},
		}
	}
}
