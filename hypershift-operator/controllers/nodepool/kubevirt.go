package nodepool

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

const (
	rhcosOpenStackChecksumParam string = "{rhcos:openstack:checksum}"
	rhcosOpenStackURLParam      string = "{rhcos:openstack:url}"
)

func kubevirtMachineTemplateSpec(nodePool *hyperv1.NodePool, rhcosInfo kubevirtRHCOSDetails) *capikubevirt.KubevirtMachineTemplateSpec {

	template := nodePool.Spec.Platform.Kubevirt.NodeTemplate.DeepCopy()

	dataVolumeTemplates := template.Spec.DataVolumeTemplates
	for i, dv := range dataVolumeTemplates {
		if dv.Spec.Source != nil && dv.Spec.Source.Registry != nil &&
			dv.Spec.Source.Registry.URL != nil && strings.Contains(*dv.Spec.Source.Registry.URL, rhcosOpenStackChecksumParam) {
			image := strings.ReplaceAll(*dv.Spec.Source.Registry.URL, rhcosOpenStackChecksumParam, rhcosInfo.Checksum)
			dataVolumeTemplates[i].Spec.Source.Registry.URL = &image
		}
		if dv.Spec.Source != nil && dv.Spec.Source.HTTP != nil &&
			strings.Contains(dv.Spec.Source.HTTP.URL, rhcosOpenStackURLParam) {
			url := strings.ReplaceAll(dv.Spec.Source.HTTP.URL, rhcosOpenStackURLParam, rhcosInfo.OpenStackDownloadURL)
			dataVolumeTemplates[i].Spec.Source.HTTP.URL = url
		}
	}
	volumes := template.Spec.Template.Spec.Volumes
	for i, volume := range volumes {
		if volume.ContainerDisk != nil && strings.Contains(volume.ContainerDisk.Image, rhcosOpenStackChecksumParam) {
			volumes[i].ContainerDisk.Image = strings.ReplaceAll(volume.ContainerDisk.Image, rhcosOpenStackChecksumParam, rhcosInfo.Checksum)
		}
	}
	return &capikubevirt.KubevirtMachineTemplateSpec{
		Template: capikubevirt.KubevirtMachineTemplateResource{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *template,
			},
		},
	}
}
