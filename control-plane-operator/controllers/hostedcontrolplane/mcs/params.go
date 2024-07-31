package mcs

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type MCSParams struct {
	OwnerRef          config.OwnerRef
	RootCA            *corev1.Secret
	KubeletClientCA   *corev1.ConfigMap
	UserCA            *corev1.ConfigMap
	PullSecret        *corev1.Secret
	DNS               *configv1.DNS
	Infrastructure    *configv1.Infrastructure
	Network           *configv1.Network
	Proxy             *configv1.Proxy
	Image             *configv1.Image
	InstallConfig     *globalconfig.InstallConfig
	ConfigurationHash string
}

func NewMCSParams(hcp *hyperv1.HostedControlPlane, rootCA, pullSecret *corev1.Secret, userCA, kubeletClientCA *corev1.ConfigMap) (*MCSParams, error) {
	dns := globalconfig.DNSConfig()
	globalconfig.ReconcileDNSConfig(dns, hcp)

	infra := globalconfig.InfrastructureConfig()
	if err := globalconfig.ReconcileInfrastructure(infra, hcp); err != nil {
		return &MCSParams{}, fmt.Errorf("failed on infrastructure reconciliation config: %w", err)
	}

	network := globalconfig.NetworkConfig()
	if err := globalconfig.ReconcileNetworkConfig(network, hcp); err != nil {
		return &MCSParams{}, fmt.Errorf("failed on network reconciliation config: %w", err)

	}

	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatus(proxy, hcp)

	image := globalconfig.ImageConfig()
	globalconfig.ReconcileImageConfig(image, hcp)

	hcConfigurationHash, err := util.HashStruct(hcp.Spec.Configuration)
	if err != nil {
		return &MCSParams{}, fmt.Errorf("failed to hash HCP configuration: %w", err)
	}

	return &MCSParams{
		OwnerRef:          config.OwnerRefFrom(hcp),
		RootCA:            rootCA,
		KubeletClientCA:   kubeletClientCA,
		UserCA:            userCA,
		PullSecret:        pullSecret,
		DNS:               dns,
		Infrastructure:    infra,
		Network:           network,
		Proxy:             proxy,
		Image:             image,
		InstallConfig:     globalconfig.NewInstallConfig(hcp),
		ConfigurationHash: hcConfigurationHash,
	}, nil
}
