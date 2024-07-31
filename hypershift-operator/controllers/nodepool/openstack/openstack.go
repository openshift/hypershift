package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"

	openstackutil "github.com/openshift/hypershift/support/openstackutil"
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

	port := capiopenstack.PortOpts{}

	var machineNetworks []hyperv1.MachineNetworkEntry
	if hcluster.Spec.Networking.MachineNetwork == nil || len(hcluster.Spec.Networking.MachineNetwork) == 0 {
		machineNetworks = []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR(openstackutil.DefaultCIDRBlock)}}
	} else {
		machineNetworks = hcluster.Spec.Networking.MachineNetwork
	}
	ingressIP, err := openstackutil.GetIngressIP(machineNetworks[0])
	if err != nil {
		return nil, err
	}
	port.AllowedAddressPairs = []capiopenstack.AddressPair{
		{
			// Allows Ingress VIP traffic on that port
			IPAddress: ingressIP,
		},
	}
	openStackMachineTemplate.Template.Spec.Ports = []capiopenstack.PortOpts{port}

	return openStackMachineTemplate, nil
}
