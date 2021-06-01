package config

import (
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
	DefaultServiceNodePortRange = "30000-32767"
)

func Network(hcp *hyperv1.HostedControlPlane) configv1.Network {
	return configv1.Network{
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{
					CIDR: hcp.Spec.PodCIDR,
					// TODO: Wire in the host prefix in addition to CIDR
					HostPrefix: 23,
				},
			},
			ServiceNetwork: []string{
				hcp.Spec.ServiceCIDR,
			},
			NetworkType: "OpenShiftSDN",
			// TODO: Allow configuration of external IP for bare metal
			ExternalIP:           nil,
			ServiceNodePortRange: DefaultServiceNodePortRange,
		},
	}
}

func ClusterCIDR(network *configv1.Network) string {
	for _, entry := range network.Spec.ClusterNetwork {
		return entry.CIDR
	}
	return ""
}

func ServiceCIDR(network *configv1.Network) string {
	for _, entry := range network.Spec.ServiceNetwork {
		return entry
	}
	return ""
}
