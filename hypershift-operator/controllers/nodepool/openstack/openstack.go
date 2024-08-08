package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/hypershift/support/openstackutil"
	utilpointer "k8s.io/utils/pointer"
	capiopenstack "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

func MachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (*capiopenstack.OpenStackMachineTemplateSpec, error) {
	openStackMachineTemplate := &capiopenstack.OpenStackMachineTemplateSpec{Template: capiopenstack.OpenStackMachineTemplateResource{Spec: capiopenstack.OpenStackMachineSpec{
		Flavor: nodePool.Spec.Platform.OpenStack.Flavor,
	}}}

	if nodePool.Spec.Platform.OpenStack.ImageName != "" {
		openStackMachineTemplate.Template.Spec.Image.Filter = &capiopenstack.ImageFilter{
			Name: utilpointer.String(nodePool.Spec.Platform.OpenStack.ImageName),
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

		// create "ports" which is a list of PortOpts and has an empty item
		ports := []capiopenstack.PortOpts{{}}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, ports...)

		additionalPorts := make([]capiopenstack.PortOpts, len(nodePool.Spec.Platform.OpenStack.AdditionalPorts))
		for i, port := range nodePool.Spec.Platform.OpenStack.AdditionalPorts {
			additionalPorts[i] = capiopenstack.PortOpts{}
			port.Description = "Additional port for Hypershift node pool " + nodePool.Name
			if port.Network != nil {
				additionalPorts[i].Network = &capiopenstack.NetworkParam{}
				if port.Network.Filter != nil {
					additionalPorts[i].Network.Filter = openstackutil.CreateCAPONetworkFilter(port.Network.Filter)
				}
				if port.Network.ID != nil {
					additionalPorts[i].Network.ID = port.Network.ID
				}
			}
			if port.Description != "" {
				additionalPorts[i].Description = &port.Description
			}
			if len(port.AllowedAddressPairs) > 0 {
				additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs = []capiopenstack.AddressPair{}
				for _, allowedAddressPair := range port.ResolvedPortSpecFields.AllowedAddressPairs {
					additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs = append(additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs, capiopenstack.AddressPair{
						IPAddress: allowedAddressPair.IPAddress,
					})
				}
			}
			if port.ResolvedPortSpecFields.VNICType != "" {
				additionalPorts[i].ResolvedPortSpecFields.VNICType = &port.ResolvedPortSpecFields.VNICType
			}
			if port.ResolvedPortSpecFields.DisablePortSecurity {
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = &port.ResolvedPortSpecFields.DisablePortSecurity
			}
		}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, additionalPorts...)
	}
	return openStackMachineTemplate, nil
}
