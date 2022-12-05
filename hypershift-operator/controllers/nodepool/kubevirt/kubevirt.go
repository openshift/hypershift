package kubevirt

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
)

func defaultImage(releaseImage *releaseinfo.ReleaseImage) (string, string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x64_64")
	}
	openStack, exists := arch.Artifacts["openstack"]
	if !exists {
		return "", "", fmt.Errorf("couldn't find OS metadata for openstack")
	}
	artifact, exists := openStack.Formats["qcow2.gz"]
	if !exists {
		return "", "", fmt.Errorf("couldn't find OS metadata for openstack qcow2.gz")
	}
	disk, exists := artifact["disk"]
	if !exists {
		return "", "", fmt.Errorf("couldn't find OS metadata for the openstack qcow2.gz disk")
	}

	return disk.Location, disk.SHA256, nil
}

func GetImage(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage, hostedNamespace string) (BootImage, error) {
	var rootVolume *hyperv1.KubevirtRootVolume
	if nodePool.Spec.Platform.Kubevirt != nil {
		rootVolume = nodePool.Spec.Platform.Kubevirt.RootVolume
	}

	if rootVolume != nil &&
		rootVolume.Image != nil &&
		rootVolume.Image.ContainerDiskImage != nil {

		imageName := *nodePool.Spec.Platform.Kubevirt.RootVolume.Image.ContainerDiskImage

		return newContainerBootImage(imageName), nil
	}

	imageName, imageHash, err := defaultImage(releaseImage)
	if err != nil {
		return nil, err
	}

	// KubeVirt Caching is disabled by default
	if rootVolume != nil && rootVolume.CacheStrategy != nil && rootVolume.CacheStrategy.Type == hyperv1.KubevirtCachingStrategyPVC {
		return newCachedQCOWBootImage(imageName, imageHash, hostedNamespace), nil
	}

	return newQCOWBootImage(imageName), nil
}

func PlatformValidation(nodePool *hyperv1.NodePool) error {
	kvPlatform := nodePool.Spec.Platform.Kubevirt
	if kvPlatform == nil {
		return fmt.Errorf("nodepool.spec.platform.kubevirt is required")
	} else if kvPlatform.RootVolume == nil {
		return fmt.Errorf("the kubevirt platform rootVolume field is required")
	}

	if kvPlatform.RootVolume.Type == hyperv1.KubevirtVolumeTypePersistent {
		if kvPlatform.RootVolume.Persistent == nil {
			return fmt.Errorf("the kubevirt persistent storage field must be set when rootVolume.Type=Persistent")
		}
	}

	return nil
}

func virtualMachineTemplateBase(nodePool *hyperv1.NodePool, bootImage BootImage) *capikubevirt.VirtualMachineTemplateSpec {
	const rootVolumeName = "rhcos"

	var (
		memory apiresource.Quantity
		cores  uint32
	)

	dvSource := bootImage.getDVSourceForVMTemplate()

	runAlways := kubevirtv1.RunStrategyAlways
	kvPlatform := nodePool.Spec.Platform.Kubevirt

	if kvPlatform.Compute != nil {
		if kvPlatform.Compute.Memory != nil {
			memory = *kvPlatform.Compute.Memory
		}
		if kvPlatform.Compute.Cores != nil {
			cores = *kvPlatform.Compute.Cores
		}
	}

	template := &capikubevirt.VirtualMachineTemplateSpec{
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: &runAlways,
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						CPU:    &kubevirtv1.CPU{Cores: cores},
						Memory: &kubevirtv1.Memory{Guest: &memory},
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
	}

	template.Spec.Template.Spec.Domain.Devices.Disks = []kubevirtv1.Disk{
		{
			Name: rootVolumeName,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: "virtio",
				},
			},
		},
	}

	template.Spec.Template.Spec.Volumes = []kubevirtv1.Volume{
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
			Source: dvSource,
		},
	}

	if kvPlatform.RootVolume != nil {
		if kvPlatform.RootVolume.Persistent != nil {
			storageSpec := &v1beta1.StorageSpec{}

			for _, ac := range kvPlatform.RootVolume.Persistent.AccessModes {
				storageSpec.AccessModes = append(storageSpec.AccessModes, corev1.PersistentVolumeAccessMode(ac))
			}

			if kvPlatform.RootVolume.Persistent.Size != nil {
				storageSpec.Resources = corev1.ResourceRequirements{
					Requests: map[corev1.ResourceName]apiresource.Quantity{
						corev1.ResourceStorage: *kvPlatform.RootVolume.Persistent.Size,
					},
				}
			}

			if kvPlatform.RootVolume.Persistent.StorageClass != nil {
				storageSpec.StorageClassName = kvPlatform.RootVolume.Persistent.StorageClass
			}

			dataVolume.Spec.Storage = storageSpec
		}
	}
	template.Spec.DataVolumeTemplates = []kubevirtv1.DataVolumeTemplateSpec{dataVolume}

	return template
}

func MachineTemplateSpec(nodePool *hyperv1.NodePool, bootImage BootImage, hcluster *hyperv1.HostedCluster) *capikubevirt.KubevirtMachineTemplateSpec {
	vmTemplate := virtualMachineTemplateBase(nodePool, bootImage)

	vmTemplate.Spec.Template.Spec.Affinity = &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: int32(100),
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      hyperv1.NodePoolNameLabel,
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{nodePool.Name},
								},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		},
	}

	if vmTemplate.ObjectMeta.Labels == nil {
		vmTemplate.ObjectMeta.Labels = map[string]string{}
	}
	if vmTemplate.Spec.Template.ObjectMeta.Labels == nil {
		vmTemplate.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}

	vmTemplate.Spec.Template.ObjectMeta.Labels[hyperv1.NodePoolNameLabel] = nodePool.Name
	vmTemplate.Spec.Template.ObjectMeta.Labels[hyperv1.InfraIDLabel] = hcluster.Spec.InfraID

	vmTemplate.ObjectMeta.Labels[hyperv1.NodePoolNameLabel] = nodePool.Name
	vmTemplate.ObjectMeta.Labels[hyperv1.InfraIDLabel] = hcluster.Spec.InfraID

	if hcluster.Spec.Platform.Kubevirt != nil && hcluster.Spec.Platform.Kubevirt.Credentials != nil {
		vmTemplate.ObjectMeta.Namespace = hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace
	}

	return &capikubevirt.KubevirtMachineTemplateSpec{
		Template: capikubevirt.KubevirtMachineTemplateResource{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *vmTemplate,
			},
		},
	}
}
