package mcs

import (
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
)

type MCSParams struct {
	OwnerRef       config.OwnerRef
	RootCA         *corev1.Secret
	CombinedCA     *corev1.ConfigMap
	UserCA         *corev1.ConfigMap
	PullSecret     *corev1.Secret
	DNS            *configv1.DNS
	Infrastructure *configv1.Infrastructure
	Network        *configv1.Network
	Proxy          *configv1.Proxy
	InstallConfig  *globalconfig.InstallConfig
}

func NewMCSParams(hcp *hyperv1.HostedControlPlane, rootCA, pullSecret *corev1.Secret, combinedCA, userCA *corev1.ConfigMap) *MCSParams {
	dns := globalconfig.DNSConfig()
	globalconfig.ReconcileDNSConfig(dns, hcp)

	infra := globalconfig.InfrastructureConfig()
	globalconfig.ReconcileInfrastructure(infra, hcp)

	network := globalconfig.NetworkConfig()
	globalconfig.ReconcileNetworkConfig(network, hcp)

	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatus(proxy, hcp)

	return &MCSParams{
		OwnerRef:       config.OwnerRefFrom(hcp),
		RootCA:         rootCA,
		CombinedCA:     combinedCA,
		UserCA:         userCA,
		PullSecret:     pullSecret,
		DNS:            dns,
		Infrastructure: infra,
		Network:        network,
		Proxy:          proxy,
		InstallConfig:  globalconfig.NewInstallConfig(hcp),
	}
}
