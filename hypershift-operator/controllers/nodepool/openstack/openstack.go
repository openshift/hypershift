package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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
	if len(hcluster.Spec.Platform.OpenStack.Subnets) == 0 {
		// When not using BYON, we will create one network and one subnet.
		// So the number of ports will be the addition of the number of additional networks and 1.
		portsLength := 1 + len(nodePool.Spec.Platform.OpenStack.AdditionalNetworks)
		ports := make([]capiopenstack.PortOpts, portsLength)
		ports = append(ports, capiopenstack.PortOpts{
			Network: &capiopenstack.NetworkParam{
				Filter: &capiopenstack.NetworkFilter{
					// TODO(emilien): This is dangerous, we should change that at some point.
					// This could break if CAPO decides to name the network differently.
					Name: "k8s-clusterapi-cluster-" + hcluster.Namespace + hcluster.Name + hcluster.Spec.InfraID,
				},
			},
		})
		for _, network := range nodePool.Spec.Platform.OpenStack.AdditionalNetworks {
			ports = append(ports, capiopenstack.PortOpts{
				Network: &capiopenstack.NetworkParam{
					ID: network.ID,
				},
			})
			// TODO: add filters
			// TODO: add vnic-type for SRIOV
		}
		openStackMachineTemplate.Template.Spec.Ports = ports
	}
	return openStackMachineTemplate, nil
}
