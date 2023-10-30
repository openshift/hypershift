package util

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func MachineCIDRs(machineNetwork []hyperv1.MachineNetworkEntry) []string {
	var cidrs []string
	for _, entry := range machineNetwork {
		cidrs = append(cidrs, entry.CIDR.String())
	}
	return cidrs
}

func FirstMachineCIDR(machineNetwork []hyperv1.MachineNetworkEntry) string {
	machineCIDRs := MachineCIDRs(machineNetwork)
	if len(machineCIDRs) > 0 {
		return machineCIDRs[0]
	}
	return ""
}

func ServiceCIDRs(serviceNetwork []hyperv1.ServiceNetworkEntry) []string {
	var cidrs []string
	for _, entry := range serviceNetwork {
		cidrs = append(cidrs, entry.CIDR.String())
	}
	return cidrs
}

func FirstServiceCIDR(serviceNetwork []hyperv1.ServiceNetworkEntry) string {
	serviceCIDRs := ServiceCIDRs(serviceNetwork)
	if len(serviceCIDRs) > 0 {
		return serviceCIDRs[0]
	}
	return ""
}

func ClusterCIDRs(clusterNetwork []hyperv1.ClusterNetworkEntry) []string {
	var cidrs []string
	for _, entry := range clusterNetwork {
		cidrs = append(cidrs, entry.CIDR.String())
	}
	return cidrs
}

func FirstClusterCIDR(clusterNetwork []hyperv1.ClusterNetworkEntry) string {
	clusterCIDRs := ClusterCIDRs(clusterNetwork)
	if len(clusterCIDRs) > 0 {
		return clusterCIDRs[0]
	}
	return ""
}

func APIPort(hcp *hyperv1.HostedControlPlane) *int32 {
	if hcp != nil && hcp.Spec.Networking.APIServer != nil {
		return hcp.Spec.Networking.APIServer.Port
	}
	return nil
}

// BindAPIPortWithDefault will retrieve the port the kube-apiserver binds on locally in the pod.
// This comes from hcp.Spec.Networking.APIServer.Port if set and != 443
func BindAPIPortWithDefault(hcp *hyperv1.HostedControlPlane, defaultValue int32) int32 {
	// Binding on 443 is not allowed. So returning default for that case.
	// This provides backward compatibility for existing clusters which were defaulting to that value, ignoring it here and
	// enforcing it in the data plane proxy by reconciling the endpoint. 443 API input is not allowed now.
	// https://github.com/openshift/hypershift/pull/2964
	if hcp.Spec.Networking.APIServer != nil && hcp.Spec.Networking.APIServer.Port != nil && *hcp.Spec.Networking.APIServer.Port != 443 {
		return *hcp.Spec.Networking.APIServer.Port
	}
	return defaultValue
}

// BindAPIPortWithDefaultFromHostedCluster will retrieve the port the kube-apiserver binds on locally in the pod.
// This comes from hcp.Spec.Networking.APIServer.Port if set and != 443
func BindAPIPortWithDefaultFromHostedCluster(hc *hyperv1.HostedCluster, defaultValue int32) int32 {
	// Binding on 443 is not allowed. So returning default for that case.
	// This provides backward compatibility for existing clusters which were defaulting to that value, ignoring it here and
	// enforcing it in the data plane proxy by reconciling the endpoint. 443 API input is not allowed now.
	// https://github.com/openshift/hypershift/pull/2964
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.Port != nil && *hc.Spec.Networking.APIServer.Port != 443 {
		return *hc.Spec.Networking.APIServer.Port
	}
	return defaultValue
}

// InternalAPIPortWithDefault will retrieve the port to use to contact the APIServer over the Kubernetes service domain
// kube-apiserver.NAMESPACE.svc.cluster.local:INTERNAL_API_PORT
func InternalAPIPortWithDefault(hcp *hyperv1.HostedControlPlane, defaultValue int32) int32 {
	// TODO (alberto): Why is the exposed port for the SVC coming from .Spec.Networking.APIServer.Port?
	// The API input is meant to be just the KAS Pod Port (and so the nodes haproxy).
	if port := APIPort(hcp); port != nil {
		return *port
	}
	return defaultValue
}

func AdvertiseAddress(hcp *hyperv1.HostedControlPlane) *string {
	if hcp != nil && hcp.Spec.Networking.APIServer != nil {
		return hcp.Spec.Networking.APIServer.AdvertiseAddress
	}
	return nil
}

func AdvertiseAddressWithDefault(hcp *hyperv1.HostedControlPlane, defaultValue string) string {
	if address := AdvertiseAddress(hcp); address != nil {
		return *address
	}
	return defaultValue
}

func AllowedCIDRBlocks(hcp *hyperv1.HostedControlPlane) []hyperv1.CIDRBlock {
	if hcp != nil && hcp.Spec.Networking.APIServer != nil {
		return hcp.Spec.Networking.APIServer.AllowedCIDRBlocks
	}
	return nil
}

func GetAdvertiseAddress(hcp *hyperv1.HostedControlPlane, ipv4DefaultAddress, ipv6DefaultAddress string) string {
	var advertiseAddress string

	ipv4, err := IsIPv4(hcp.Spec.Networking.ServiceNetwork[0].CIDR.String())
	if err != nil || ipv4 {
		if address := AdvertiseAddressWithDefault(hcp, ipv4DefaultAddress); len(address) > 0 {
			advertiseAddress = address
		}
	} else {
		if address := AdvertiseAddressWithDefault(hcp, ipv6DefaultAddress); len(address) > 0 {
			advertiseAddress = address
		}
	}

	return advertiseAddress
}
