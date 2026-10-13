package globalconfig

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

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

// TODO (relyt0925): this is is utilized by the machine config server and feeds into the user data setup to download
// ignition configuration. It also factors into the machine config served to the machine. These usages need to be
// further examined before it directly takes what the user configures for the cluster-wide proxy at the SDN level.
// In current state of the world: the in cluster proxy configuration set by the user is never picked up (only the one
// set on the hosted cluster instance).
func ReconcileProxyConfig(cfg *configv1.Proxy, hcfg *hyperv1.ClusterConfiguration) {
	spec := configv1.ProxySpec{}
	if hcfg != nil && hcfg.Proxy != nil {
		spec = *hcfg.Proxy
	}

	cfg.Spec = spec
}

// ReconcileInClusterProxyConfig will reconcile the proxy configured in the hosted cluster spec when specified otherwise
// will maintain what the user specifies in the proxy spec by default
func ReconcileInClusterProxyConfig(cfg *configv1.Proxy, hcfg *hyperv1.ClusterConfiguration) {
	const hostedClusterProxyConfigDefinedAnnotation = "hypershift.io/hosted-cluster-proxy-config"
	if hcfg != nil && hcfg.Proxy != nil {
		if cfg.Annotations == nil {
			cfg.Annotations = map[string]string{}
		}
		cfg.Annotations[hostedClusterProxyConfigDefinedAnnotation] = "true"
		cfg.Spec = *hcfg.Proxy
	} else {
		if _, exists := cfg.Annotations[hostedClusterProxyConfigDefinedAnnotation]; exists {
			//clear out spec after proxy configuration removed from HostedClusterProxy
			cfg.Spec = configv1.ProxySpec{}
			delete(cfg.Annotations, hostedClusterProxyConfigDefinedAnnotation)
		}
	}
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
