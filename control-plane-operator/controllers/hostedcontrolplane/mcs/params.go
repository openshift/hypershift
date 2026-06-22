package mcs

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/backwardcompat"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
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
	globalconfig.ReconcileInfrastructure(infra, hcp)

	network := globalconfig.NetworkConfig()
	if err := globalconfig.ReconcileNetworkConfig(network, hcp); err != nil {
		return &MCSParams{}, fmt.Errorf("failed on network reconciliation config: %w", err)

	}

	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatus(proxy, hcp)

	image := globalconfig.ImageConfig()
	globalconfig.ReconcileImageConfig(image, hcp)

	// Some fields in the ClusterConfiguration have changes that are not backwards compatible with older versions of the CPO.
	hcConfigurationHash, err := backwardcompat.GetBackwardCompatibleConfigHash(hcp.Spec.Configuration)
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
