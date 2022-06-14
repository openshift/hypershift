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

func ReconcileNetworkConfig(cfg *configv1.Network, hcp *hyperv1.HostedControlPlane) {
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
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Network != nil {
		cfg.Spec.ExternalIP = hcp.Spec.Configuration.Network.ExternalIP
		cfg.Spec.ServiceNodePortRange = hcp.Spec.Configuration.Network.ServiceNodePortRange
	}
}
