package globalconfig

import (
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ProxyConfig() *configv1.Proxy {
	return &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileProxyConfig(cfg *configv1.Proxy, hcp *hyperv1.HostedControlPlane, globalConfig GlobalConfig) {
	if globalConfig.Proxy != nil {
		cfg.Spec = globalConfig.Proxy.Spec
	}
}
