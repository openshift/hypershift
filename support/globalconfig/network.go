package globalconfig

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func NetworkConfig() *configv1.Network {
	return &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileNetworkConfig(cfg *configv1.Network, hcp *hyperv1.HostedControlPlane, globalConfig GlobalConfig) {
	for _, entry := range hcp.Spec.ClusterNetwork {
		cfg.Spec.ClusterNetwork = append(cfg.Spec.ClusterNetwork, configv1.ClusterNetworkEntry{
			CIDR:       entry.CIDR.String(),
			HostPrefix: uint32(entry.HostPrefix),
		})
	}
	cfg.Spec.NetworkType = string(hcp.Spec.NetworkType)
	cfg.Spec.ServiceNetwork = hcp.Spec.ServiceNetwork.IPNets().StringSlice()
	if globalConfig.Network != nil {
		cfg.Spec.ExternalIP = globalConfig.Network.Spec.ExternalIP
		cfg.Spec.ServiceNodePortRange = globalConfig.Network.Spec.ServiceNodePortRange
	}
}
