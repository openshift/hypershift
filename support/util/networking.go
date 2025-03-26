package util

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func MachineCIDRs(machineNetwork []hyperv1.MachineNetworkEntry) []string {
	var cidrs []string
	for _, entry := range machineNetwork {
		cidrs = append(cidrs, entry.CIDR.String())
	}
	return cidrs
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

// MachineNetworksToList converts a list of MachineNetworkEntry to a comma separated list of CIDRs.
func MachineNetworksToList(machineNetwork []hyperv1.MachineNetworkEntry) string {
	cidrs := []string{}
	for _, mn := range machineNetwork {
		cidrs = append(cidrs, mn.CIDR.String())
	}
	return strings.Join(cidrs, ",")
}

// KASPodPort will retrieve the port the kube-apiserver binds on locally in the pod.
// This comes from hcp.Spec.Networking.APIServer.Port if set and != 443 or defaults to 6443.
func KASPodPort(hcp *hyperv1.HostedControlPlane) int32 {
	// Binding on 443 is not allowed. So returning default for that case.
	// This provides backward compatibility for existing clusters which were defaulting to that value, ignoring it here and
	// enforcing it in the data plane proxy by reconciling the endpoint. 443 API input is not allowed now.
	// https://github.com/openshift/hypershift/pull/2964
	if hcp.Spec.Networking.APIServer != nil && hcp.Spec.Networking.APIServer.Port != nil && *hcp.Spec.Networking.APIServer.Port != 443 {
		return *hcp.Spec.Networking.APIServer.Port
	}
	return 6443
}

// KASPodPortFromHostedCluster will retrieve the port the kube-apiserver binds on locally in the pod.
// This comes from hcp.Spec.Networking.APIServer.Port if set and != 443 or defaults to 6443.
func KASPodPortFromHostedCluster(hc *hyperv1.HostedCluster) int32 {
	// Binding on 443 is not allowed. So returning default for that case.
	// This provides backward compatibility for existing clusters which were defaulting to that value, ignoring it here and
	// enforcing it in the data plane proxy by reconciling the endpoint. 443 API input is not allowed now.
	// https://github.com/openshift/hypershift/pull/2964
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.Port != nil && *hc.Spec.Networking.APIServer.Port != 443 {
		return *hc.Spec.Networking.APIServer.Port
	}
	return 6443
}

// APIPortForLocalZone returns the port used by processes within a private hosted cluster
// to communicate with the KAS via the api.<hc-name>.hypershift.local host.
func APIPortForLocalZone(isLBKAS bool) int32 {
	if isLBKAS {
		return 6443
	}
	return 443
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
	var ipv4 bool
	var err error

	if len(hcp.Spec.Networking.ServiceNetwork) > 0 {
		ipv4, err = IsIPv4CIDR(hcp.Spec.Networking.ServiceNetwork[0].CIDR.String())
	} else {
		ipv4 = true
	}
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
