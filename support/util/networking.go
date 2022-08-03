package util

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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

func APIPortWithDefault(hcp *hyperv1.HostedControlPlane, defaultValue int32) int32 {
	if port := APIPort(hcp); port != nil {
		return *port
	}
	return defaultValue
}

func APIPortWithDefaultFromHostedCluster(hc *hyperv1.HostedCluster, defaultValue int32) int32 {
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.Port != nil {
		return *hc.Spec.Networking.APIServer.Port
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
