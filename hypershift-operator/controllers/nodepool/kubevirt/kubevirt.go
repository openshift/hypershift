package kubevirt

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func defaultImage(releaseImage *releaseinfo.ReleaseImage) (string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x64_64")
	}
	openStack, exists := arch.Artifacts["openstack"]
	if !exists {
		return "", fmt.Errorf("couldn't find OS metadata for openstack")
	}
	artifact, exists := openStack.Formats["qcow2.gz"]
	if !exists {
		return "", fmt.Errorf("couldn't find OS metadata for openstack qcow2.gz")
	}
	disk, exists := artifact["disk"]
	if !exists {
		return "", fmt.Errorf("couldn't find OS metadata for the openstack qcow2.gz disk")
	}

	return disk.Location, nil
}

func GetImage(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	if nodePool.Spec.Platform.Kubevirt != nil &&
		nodePool.Spec.Platform.Kubevirt.RootVolume != nil &&
		nodePool.Spec.Platform.Kubevirt.RootVolume.Image != nil &&
		nodePool.Spec.Platform.Kubevirt.RootVolume.Image.ContainerDiskImage != nil {

		return fmt.Sprintf("docker://%s", *nodePool.Spec.Platform.Kubevirt.RootVolume.Image.ContainerDiskImage), nil
	}

	return defaultImage(releaseImage)
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

func virtualMachineTemplateBase(image string, kvPlatform *hyperv1.KubevirtNodePoolPlatform) *capikubevirt.VirtualMachineTemplateSpec {

	var memory apiresource.Quantity
	var volumeSize apiresource.Quantity
	var cores uint32

	rootVolumeName := "rhcos"
	runAlways := kubevirtv1.RunStrategyAlways
	pullMethod := v1beta1.RegistryPullNode

	if kvPlatform.Compute != nil {
		if kvPlatform.Compute.Memory != nil {
			memory = *kvPlatform.Compute.Memory
		}
		if kvPlatform.Compute.Cores != nil {
			cores = *kvPlatform.Compute.Cores
		}
	}

	if kvPlatform.RootVolume != nil {
		if kvPlatform.RootVolume.Image != nil && kvPlatform.RootVolume.Image.ContainerDiskImage != nil {
			image = fmt.Sprintf("docker://%s", *kvPlatform.RootVolume.Image.ContainerDiskImage)
		}

		if kvPlatform.RootVolume.Persistent != nil && kvPlatform.RootVolume.Persistent.Size != nil {
			volumeSize = *kvPlatform.RootVolume.Persistent.Size
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
	}

	if strings.HasPrefix(image, "docker://") {
		dataVolume.Spec = v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				Registry: &v1beta1.DataVolumeSourceRegistry{
					URL:        &image,
					PullMethod: &pullMethod,
				},
			},
		}
	} else {
		dataVolume.Spec = v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				HTTP: &v1beta1.DataVolumeSourceHTTP{
					URL: image,
				},
			},
		}
	}

	dataVolume.Spec.Storage = &v1beta1.StorageSpec{
		Resources: corev1.ResourceRequirements{
			Requests: map[corev1.ResourceName]apiresource.Quantity{
				corev1.ResourceStorage: volumeSize,
			},
		},
	}

	if kvPlatform.RootVolume != nil &&
		kvPlatform.RootVolume.Persistent != nil {
		if kvPlatform.RootVolume.Persistent.StorageClass != nil {
			dataVolume.Spec.Storage.StorageClassName = kvPlatform.RootVolume.Persistent.StorageClass
		}
		if len(kvPlatform.RootVolume.Persistent.AccessModes) != 0 {
			var accessModes []corev1.PersistentVolumeAccessMode
			for _, am := range kvPlatform.RootVolume.Persistent.AccessModes {
				amv1 := corev1.PersistentVolumeAccessMode(am)
				accessModes = append(accessModes, amv1)
			}
			dataVolume.Spec.Storage.AccessModes = accessModes
		}
	}

	template.Spec.DataVolumeTemplates = []kubevirtv1.DataVolumeTemplateSpec{dataVolume}

	return template
}

func MachineTemplateSpec(image string, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) *capikubevirt.KubevirtMachineTemplateSpec {
	nodePoolNameLabelKey := hyperv1.NodePoolNameLabel
	infraIDLabelKey := hyperv1.InfraIDLabel

	vmTemplate := virtualMachineTemplateBase(image, nodePool.Spec.Platform.Kubevirt)

	vmTemplate.Spec.Template.Spec.Affinity = &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: int32(100),
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      nodePoolNameLabelKey,
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

	vmTemplate.Spec.Template.ObjectMeta.Labels[nodePoolNameLabelKey] = nodePool.Name
	vmTemplate.Spec.Template.ObjectMeta.Labels[infraIDLabelKey] = hcluster.Spec.InfraID

	vmTemplate.ObjectMeta.Labels[nodePoolNameLabelKey] = nodePool.Name
	vmTemplate.ObjectMeta.Labels[infraIDLabelKey] = hcluster.Spec.InfraID

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
