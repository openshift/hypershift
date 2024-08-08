package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/hypershift/support/openstackutil"
	"k8s.io/utils/ptr"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

func MachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (*capiopenstackv1beta1.OpenStackMachineTemplateSpec, error) {
	openStackMachineTemplate := &capiopenstackv1beta1.OpenStackMachineTemplateSpec{Template: capiopenstackv1beta1.OpenStackMachineTemplateResource{Spec: capiopenstackv1beta1.OpenStackMachineSpec{
		Flavor: ptr.To(nodePool.Spec.Platform.OpenStack.Flavor),
	}}}

	if nodePool.Spec.Platform.OpenStack.ImageName != "" {
		openStackMachineTemplate.Template.Spec.Image.Filter = &capiopenstackv1beta1.ImageFilter{
			Name: ptr.To(nodePool.Spec.Platform.OpenStack.ImageName),
		}
	} else {
		// TODO(emilien): Add support for using the image from the release payload.
		// This will be possible when CAPO supports managing images in the OpenStack cluster:
		// https://github.com/kubernetes-sigs/cluster-api-provider-openstack/pull/2130
		// For 4.17 we might leave this as is and let the user provide the image name as
		// we plan to deliver the OpenStack provider as a dev preview.
		return nil, fmt.Errorf("image name is required")
	}

	// TODO: add support for BYO network/subnet
	if len(hcluster.Spec.Platform.OpenStack.Subnets) == 0 && len(nodePool.Spec.Platform.OpenStack.AdditionalPorts) > 0 {
		// Initialize the ports slice with an empty port which will be used as the primary port.
		// CAPO will figure out the network and subnet for this port since tey are not provided.
		ports := []capiopenstackv1beta1.PortOpts{{}}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, ports...)

		additionalPorts := make([]capiopenstackv1beta1.PortOpts, len(nodePool.Spec.Platform.OpenStack.AdditionalPorts))
		for i, port := range nodePool.Spec.Platform.OpenStack.AdditionalPorts {
			additionalPorts[i] = capiopenstackv1beta1.PortOpts{}
			additionalPorts[i].Description = ptr.To("Additional port for Hypershift node pool " + nodePool.Name)
			if port.Network != nil {
				additionalPorts[i].Network = &capiopenstackv1beta1.NetworkParam{}
				if port.Network.Filter != nil {
					additionalPorts[i].Network.Filter = openstackutil.CreateCAPONetworkFilter(port.Network.Filter)
				}
				if port.Network.ID != nil {
					additionalPorts[i].Network.ID = port.Network.ID
				}
			}
			for _, allowedAddressPair := range port.AllowedAddressPairs {
				additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs = []capiopenstackv1beta1.AddressPair{}
				additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs = append(additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs, capiopenstackv1beta1.AddressPair{
					IPAddress: allowedAddressPair.IPAddress,
				})
			}
			if port.VNICType != "" {
				additionalPorts[i].ResolvedPortSpecFields.VNICType = &port.VNICType
			}
			switch port.PortSecurityPolicy {
			case hyperv1.PortSecurityEnabled:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			case hyperv1.PortSecurityDisabled:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(true)
			case hyperv1.PortSecurityDefault:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			default:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			}
		}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, additionalPorts...)
	}
	return openStackMachineTemplate, nil
}
