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
	cfg.Spec.ClusterNetwork = []configv1.ClusterNetworkEntry{
		{
			CIDR: hcp.Spec.PodCIDR,
			// TODO: expose this in the API
			HostPrefix: 23,
		},
	}
	cfg.Spec.NetworkType = string(hcp.Spec.NetworkType)
	cfg.Spec.ServiceNetwork = []string{
		hcp.Spec.ServiceCIDR,
	}
	if globalConfig.Network != nil {
		cfg.Spec.ExternalIP = globalConfig.Network.Spec.ExternalIP
		cfg.Spec.ServiceNodePortRange = globalConfig.Network.Spec.ServiceNodePortRange
	}
}
