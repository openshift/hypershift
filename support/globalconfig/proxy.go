package globalconfig

import (
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func ProxyConfig() *configv1.Proxy {
	return &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileProxyConfig(cfg *configv1.Proxy, hcfg *hyperv1.ClusterConfiguration) {
	spec := configv1.ProxySpec{}
	if hcfg != nil && hcfg.Proxy != nil {
		spec = *hcfg.Proxy
	}

	cfg.Spec = spec
}

func ReconcileProxyConfigWithStatus(cfg *configv1.Proxy, hcp *hyperv1.HostedControlPlane) {
	ReconcileProxyConfig(cfg, hcp.Spec.Configuration)
	defaultProxyStatus(cfg, hcp.Spec.Networking.MachineNetwork, hcp.Spec.Networking.ClusterNetwork, hcp.Spec.Networking.ServiceNetwork, hcp.Spec.Platform)
}

func ReconcileProxyConfigWithStatusFromHostedCluster(cfg *configv1.Proxy, hc *hyperv1.HostedCluster) {
	ReconcileProxyConfig(cfg, hc.Spec.Configuration)
	defaultProxyStatus(cfg, hc.Spec.Networking.MachineNetwork, hc.Spec.Networking.ClusterNetwork, hc.Spec.Networking.ServiceNetwork, hc.Spec.Platform)
}

// defaultProxyStatus does what the name suggests. It is needed to fill in no_proxy sensibly and because the ignition rendering will ignore the proxy
// config if the status is empty: https://github.com/openshift/machine-config-operator/blob/5f21537c5743d9a834936ea4eacd4691404a4958/pkg/operator/render.go#L174
// This code effectifely duplicates logic from the CNO because we need this data before the controlplane is up and from the hypershift operator which shouldn't
// access guest cluster apiservers. CNO code: https://github.com/openshift/cluster-network-operator/blob/a0e506ca7d323493afd1ff32f8366e06fd1f1c59/pkg/util/proxyconfig/no_proxy.go#L22
// We might consider updating the CNO proxy controller to manage this.
func defaultProxyStatus(p *configv1.Proxy, machineNetwork []hyperv1.MachineNetworkEntry, clusterNetwork []hyperv1.ClusterNetworkEntry, serviceNetwork []hyperv1.ServiceNetworkEntry, platform hyperv1.PlatformSpec) {
	p.Status.HTTPProxy = p.Spec.HTTPProxy
	p.Status.HTTPSProxy = p.Spec.HTTPSProxy
	if p.Spec.HTTPProxy == "" && p.Spec.HTTPSProxy == "" {
		return
	}

	set := sets.NewString(
		"127.0.0.1",
		"localhost",
		".svc",
		".cluster.local",
		// This is hypershift specific, we need it for private clusters
		".local",
	)
	for _, entry := range machineNetwork {
		set.Insert(entry.CIDR.String())
	}
	for _, entry := range clusterNetwork {
		set.Insert(entry.CIDR.String())
	}
	for _, entry := range serviceNetwork {
		set.Insert(entry.CIDR.String())
	}

	if platform.Type == hyperv1.AWSPlatform || platform.Type == hyperv1.AzurePlatform {
		set.Insert("169.254.169.254")
	}

	if platform.Type == hyperv1.AWSPlatform {
		region := platform.AWS.Region
		if region == "us-east-1" {
			set.Insert(".ec2.internal")
		} else {
			set.Insert(fmt.Sprintf(".%s.compute.internal", region))
		}
	}

	if len(p.Spec.NoProxy) > 0 {
		for _, userValue := range strings.Split(p.Spec.NoProxy, ",") {
			if userValue != "" {
				set.Insert(userValue)
			}
		}
	}

	p.Status.NoProxy = strings.Join(set.List(), ",")
}
