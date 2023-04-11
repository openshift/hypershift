package globalconfig

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/util"
)

const (
	defaultHostPrefix = 23
)

func NetworkConfig() *configv1.Network {
	return &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileNetworkConfig(cfg *configv1.Network, hcp *hyperv1.HostedControlPlane) {
	var clusterNetwork []configv1.ClusterNetworkEntry
	for _, entry := range hcp.Spec.Networking.ClusterNetwork {
		hostPrefix := uint32(entry.HostPrefix)
		if hostPrefix == 0 {
			hostPrefix = defaultHostPrefix
		}
		clusterNetwork = append(clusterNetwork, configv1.ClusterNetworkEntry{
			CIDR:       entry.CIDR.String(),
			HostPrefix: hostPrefix,
		})
	}
	cfg.Spec.ClusterNetwork = clusterNetwork
	cfg.Spec.NetworkType = string(hcp.Spec.Networking.NetworkType)
	cfg.Spec.ServiceNetwork = util.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork)
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Network != nil {
		cfg.Spec.ExternalIP = hcp.Spec.Configuration.Network.ExternalIP
		cfg.Spec.ServiceNodePortRange = hcp.Spec.Configuration.Network.ServiceNodePortRange
	}

	// Without this, the CNOs proxy controller refuses to reconcile the proxy status
	// The CNO only populates this after MTU probing and it is required for the proxy
	// controller to populate proxy status. Proxy not being populated makes the MTU
	// probing fail if there is a proxy. Get out of the deadlock by initially populating
	// it if unset.
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Proxy != nil {
		if len(cfg.Status.ClusterNetwork) == 0 {
			cfg.Status.ClusterNetwork = cfg.Spec.ClusterNetwork
		}
		if len(cfg.Status.ServiceNetwork) == 0 {
			cfg.Status.ServiceNetwork = cfg.Spec.ServiceNetwork
		}
	}
}
