package kubevirt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo"
)

var (
	LocalStorageVolumes = []string{
		"private",
		"public",
		"sockets",
		"virt-bin-share-dir",
		"libvirt-runtime",
		"ephemeral-disks",
		"container-disks",
		"hotplug-disks",
	}
)

func defaultImage(releaseImage *releaseinfo.ReleaseImage) (string, string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x84_64")
	}

	containerImage := arch.Images.Kubevirt.DigestRef
	if containerImage == "" {
		return "", "", fmt.Errorf("no kubevirt vm disk image present in release")
	}

	split := strings.Split(containerImage, "@")
	if len(split) != 2 {
		return "", "", fmt.Errorf("no kubevirt sha digest found for vm disk image")
	}

	return containerImage, split[1], nil
}

func unsupportedOpenstackDefaultImage(releaseImage *releaseinfo.ReleaseImage) (string, string, error) {
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

func allowUnsupportedRHCOSVariants(nodePool *hyperv1.NodePool) bool {
	val, exists := nodePool.Annotations[hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation]
	if exists && strings.ToLower(val) == "true" {
		return true
	}
	return false
}

func GetImage(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage, hostedNamespace string) (BootImage, error) {
	var rootVolume *hyperv1.KubevirtRootVolume
	isHTTP := false
	if nodePool.Spec.Platform.Kubevirt != nil {
		rootVolume = nodePool.Spec.Platform.Kubevirt.RootVolume
	}

	if rootVolume != nil &&
		rootVolume.Image != nil &&
		rootVolume.Image.ContainerDiskImage != nil {

		imageName := *nodePool.Spec.Platform.Kubevirt.RootVolume.Image.ContainerDiskImage

		return newBootImage(imageName, isHTTP), nil
	}

	imageName, imageHash, err := defaultImage(releaseImage)
	if err != nil && allowUnsupportedRHCOSVariants(nodePool) {
		imageName, imageHash, err = unsupportedOpenstackDefaultImage(releaseImage)
		if err != nil {
			return nil, err
		}
		isHTTP = true
	} else if err != nil {
		return nil, err
	}

	// KubeVirt Caching is disabled by default
	if rootVolume != nil && rootVolume.CacheStrategy != nil && rootVolume.CacheStrategy.Type == hyperv1.KubevirtCachingStrategyPVC {
		return newCachedBootImage(imageName, imageHash, hostedNamespace, isHTTP), nil
	}

	return newBootImage(imageName, isHTTP), nil
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

	if len(kvPlatform.AdditionalNetworks) == 0 && kvPlatform.AttachDefaultNetwork != nil && !*kvPlatform.AttachDefaultNetwork {
		return fmt.Errorf("default network cannot be disabled when no additional networks are configured")
	}

	if len(kvPlatform.KubevirtHostDevices) > 0 {
		for _, hostDevice := range kvPlatform.KubevirtHostDevices {
			if hostDevice.Count < 1 {
				return fmt.Errorf("host device count must be greater than or equal to 1. received: %d", hostDevice.Count)
			}
		}
	}

	return nil
}

func virtualMachineTemplateBase(nodePool *hyperv1.NodePool, bootImage BootImage) *capikubevirt.VirtualMachineTemplateSpec {
	const rootVolumeName = "rhcos"

	var (
		memory              apiresource.Quantity
		cores               uint32
		guaranteedResources = false
	)

	dvSource := bootImage.getDVSourceForVMTemplate()

	kvPlatform := nodePool.Spec.Platform.Kubevirt

	if kvPlatform.Compute != nil {
		if kvPlatform.Compute.Memory != nil {
			memory = *kvPlatform.Compute.Memory
		}

		if kvPlatform.Compute.Cores != nil {
			cores = *kvPlatform.Compute.Cores
		}

		guaranteedResources = kvPlatform.Compute.QosClass != nil && *kvPlatform.Compute.QosClass == hyperv1.QoSClassGuaranteed
	}

	template := &capikubevirt.VirtualMachineTemplateSpec{
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: ptr.To(kubevirtv1.RunStrategyAlways),
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kubevirt.io/allow-pod-bridge-network-live-migration": "",
					},
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Devices: kubevirtv1.Devices{
							Interfaces: virtualMachineInterfaces(kvPlatform),
						},
					},
					Networks: virtualMachineNetworks(kvPlatform),
				},
			},
		},
	}

	if guaranteedResources {
		podResources := corev1.ResourceList{
			corev1.ResourceMemory: memory,
		}

		if cores > 0 {
			podResources[corev1.ResourceCPU] = *apiresource.NewQuantity(int64(cores), apiresource.DecimalSI)
		}

		template.Spec.Template.Spec.Domain.Resources.Requests = podResources
		template.Spec.Template.Spec.Domain.Resources.Limits = podResources
	} else {
		template.Spec.Template.Spec.Domain.Memory = &kubevirtv1.Memory{Guest: &memory}
		if cores > 0 {
			template.Spec.Template.Spec.Domain.CPU = &kubevirtv1.CPU{Cores: cores}
		}
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
			Labels: map[string]string{
				hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
			},
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

			if kvPlatform.RootVolume.Persistent.VolumeMode != nil {
				storageSpec.VolumeMode = kvPlatform.RootVolume.Persistent.VolumeMode
			}

			dataVolume.Spec.Storage = storageSpec
		}
	}
	template.Spec.DataVolumeTemplates = []kubevirtv1.DataVolumeTemplateSpec{dataVolume}

	if kvPlatform.NetworkInterfaceMultiQueue != nil &&
		*nodePool.Spec.Platform.Kubevirt.NetworkInterfaceMultiQueue == hyperv1.MultiQueueEnable {
		template.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue = ptr.To(true)
	}

	if len(kvPlatform.NodeSelector) > 0 {
		template.Spec.Template.Spec.NodeSelector = kvPlatform.NodeSelector
	}

	if len(kvPlatform.KubevirtHostDevices) > 0 {
		hostDevices := []kubevirtv1.HostDevice{}
		for _, hostDevice := range kvPlatform.KubevirtHostDevices {
			for i := 1; i <= hostDevice.Count; i++ {
				kvHostDevice := kubevirtv1.HostDevice{
					Name:       "hostdevice-" + strconv.Itoa(i),
					DeviceName: hostDevice.DeviceName,
				}
				hostDevices = append(hostDevices, kvHostDevice)
			}

		}
		template.Spec.Template.Spec.Domain.Devices.HostDevices = hostDevices
	}

	return template
}

func virtualMachineInterfaceName(idx int, name string) string {
	return fmt.Sprintf("iface%d_%s", idx+1, strings.ReplaceAll(name, "/", "-"))
}

func virtualMachineInterfaces(kvPlatform *hyperv1.KubevirtNodePoolPlatform) []kubevirtv1.Interface {
	interfaces := []kubevirtv1.Interface{}
	if shouldAttachDefaultNetwork(kvPlatform) {
		interfaces = append(interfaces, kubevirtv1.Interface{
			Name: "default",
			InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
				Bridge: &kubevirtv1.InterfaceBridge{},
			},
		})
	}
	for idx, network := range kvPlatform.AdditionalNetworks {
		interfaces = append(interfaces, kubevirtv1.Interface{
			Name: virtualMachineInterfaceName(idx, network.Name),
			InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
				Bridge: &kubevirtv1.InterfaceBridge{},
			},
		})
	}
	return interfaces
}

func virtualMachineNetworks(kvPlatform *hyperv1.KubevirtNodePoolPlatform) []kubevirtv1.Network {
	networks := []kubevirtv1.Network{}
	if shouldAttachDefaultNetwork(kvPlatform) {
		networks = append(networks, kubevirtv1.Network{
			Name: "default",
			NetworkSource: kubevirtv1.NetworkSource{
				Pod: &kubevirtv1.PodNetwork{},
			},
		})
	}
	for idx, network := range kvPlatform.AdditionalNetworks {
		networks = append(networks, kubevirtv1.Network{
			Name: virtualMachineInterfaceName(idx, network.Name),
			NetworkSource: kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					NetworkName: network.Name,
				},
			},
		})
	}
	return networks
}
func shouldAttachDefaultNetwork(kvPlatform *hyperv1.KubevirtNodePoolPlatform) bool {
	return kvPlatform.AttachDefaultNetwork == nil || *kvPlatform.AttachDefaultNetwork
}

func MachineTemplateSpec(nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage, bootImage BootImage) (*capikubevirt.KubevirtMachineTemplateSpec, error) {
	if bootImage == nil {
		infraNS := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		if hcluster.Spec.Platform.Kubevirt != nil &&
			hcluster.Spec.Platform.Kubevirt.Credentials != nil &&
			len(hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace) > 0 {

			infraNS = hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace
		}
		var err error
		bootImage, err = GetImage(nodePool, releaseImage, infraNS)
		if err != nil {
			return nil, fmt.Errorf("couldn't discover a KubeVirt Image in release payload image: %w", err)
		}
	}

	vmTemplate := virtualMachineTemplateBase(nodePool, bootImage)

	// Newer versions of the NodePool controller transitioned to spreading VMs across the cluster
	// using TopologySpreadConstraints instead of Pod Anti-Affinity. When the new controller interacts
	// with a older NodePool that was previously using pod anti-affinity, we don't want to immediately
	// start using TopologySpreadConstraints because it will cause the MachineSet controller to update
	// and replace all existing VMs. For example, it would be unexpected for a user to update the
	// NodePool controller and for that to trigger a rolling update of all KubeVirt VMs.
	//
	// This annotation signals to the NodePool controller that it is safe to use TopologySpreadConstraints on a NodePool
	// without triggering an unexpected update of KubeVirt VMs.
	if _, ok := nodePool.Annotations[hyperv1.NodePoolSupportsKubevirtTopologySpreadConstraintsAnnotation]; ok {
		vmTemplate.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
			{
				MaxSkew:           1,
				TopologyKey:       "kubernetes.io/hostname",
				WhenUnsatisfiable: corev1.ScheduleAnyway,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePool.Name,
					},
				},
			},
		}
	} else {
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

	// Adding volumes for safe-eviction by clusterAutoscaler when it comes in action.
	// The volumes that should be included in the annotation are the emptyDir and hostPath ones

	if vmTemplate.Spec.Template.ObjectMeta.Annotations == nil {
		vmTemplate.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
	}

	vmTemplate.Spec.Template.ObjectMeta.Annotations[suppconfig.PodSafeToEvictLocalVolumesKey] = strings.Join(LocalStorageVolumes, ",")

	if hcluster.Spec.Platform.Kubevirt != nil && hcluster.Spec.Platform.Kubevirt.Credentials != nil {
		vmTemplate.ObjectMeta.Namespace = hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace
	}

	if err := applyJsonPatches(nodePool, hcluster, vmTemplate); err != nil {
		return nil, err
	}

	return &capikubevirt.KubevirtMachineTemplateSpec{
		Template: capikubevirt.KubevirtMachineTemplateResource{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *vmTemplate,
				BootstrapCheckSpec:     capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
			},
		},
	}, nil
}

func applyJsonPatches(nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, tmplt *capikubevirt.VirtualMachineTemplateSpec) error {
	hcAnn, hcOK := hcluster.Annotations[hyperv1.JSONPatchAnnotation]
	npAnn, npOK := nodePool.Annotations[hyperv1.JSONPatchAnnotation]

	if !hcOK && !npOK { // nothing to do
		return nil
	}

	// tmplt.Spec.Template.Spec.Networks[0].Multus.NetworkName
	buff := &bytes.Buffer{}
	dec := json.NewEncoder(buff)
	err := dec.Encode(tmplt)
	if err != nil {
		return fmt.Errorf("json: failed to encode the vm template: %w", err)
	}

	templateBytes := buff.Bytes()

	if hcOK {
		if err = applyJsonPatch(&templateBytes, hcAnn); err != nil {
			return err
		}
	}

	if npOK {
		if err = applyJsonPatch(&templateBytes, npAnn); err != nil {
			return err
		}
	}

	buff = bytes.NewBuffer(templateBytes)
	enc := json.NewDecoder(buff)

	if err = enc.Decode(tmplt); err != nil {
		return fmt.Errorf("json: failed to decode the vm template: %w", err)
	}

	return nil
}

func applyJsonPatch(tmplt *[]byte, patch string) error {
	patches, err := jsonpatch.DecodePatch([]byte(patch))
	if err != nil {
		return fmt.Errorf("failed to parse the %s annotation; wrong jsonpatch format: %w", hyperv1.JSONPatchAnnotation, err)
	}

	opts := jsonpatch.NewApplyOptions()
	opts.EnsurePathExistsOnAdd = true
	*tmplt, err = patches.ApplyWithOptions(*tmplt, opts)
	if err != nil {
		return fmt.Errorf("failed to apply json patch annotation: %w", err)
	}

	return nil
}
