package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	KonnectivityHealthPort      = 2041
	KonnectivityServerLocalPort = 8090
	KonnectivityServerPort      = 8091

	defaultMaxRequestsInflight         = 3000
	defaultMaxMutatingRequestsInflight = 1000
)

type KubeAPIServerConfigParams struct {
	ExternalIPConfig             *configv1.ExternalIPConfig
	ClusterNetwork               []string
	ServiceNetwork               []string
	NamedCertificates            []configv1.APIServerNamedServingCert
	KASPodPort                   int32
	TLSSecurityProfile           *configv1.TLSSecurityProfile
	AdditionalCORSAllowedOrigins []string
	InternalRegistryHostName     string
	ExternalRegistryHostNames    []string
	DefaultNodeSelector          string
	AdvertiseAddress             string
	ServiceAccountIssuerURL      string
	CloudProvider                string
	CloudProviderConfigRef       *corev1.LocalObjectReference
	EtcdURL                      string
	FeatureGates                 []string
	NodePortRange                string
	AuditWebhookEnabled          bool
	ConsolePublicURL             string
	DisableProfiling             bool
	APIServerSTSDirectives       string
	Authentication               *configv1.AuthenticationSpec
	MaxRequestsInflight          string
	MaxMutatingRequestsInflight  string
}

func NewConfigParams(hcp *hyperv1.HostedControlPlane) KubeAPIServerConfigParams {
	dns := globalconfig.DNSConfig()
	globalconfig.ReconcileDNSConfig(dns, hcp)

	kasConfig := KubeAPIServerConfigParams{
		ExternalIPConfig:             externalIPConfig(hcp.Spec.Configuration),
		ClusterNetwork:               util.ClusterCIDRs(hcp.Spec.Networking.ClusterNetwork),
		ServiceNetwork:               util.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork),
		NamedCertificates:            hcp.Spec.Configuration.GetNamedCertificates(),
		KASPodPort:                   util.KASPodPort(hcp),
		TLSSecurityProfile:           tlsSecurityProfile(hcp.Spec.Configuration),
		AdditionalCORSAllowedOrigins: additionalCORSAllowedOrigins(hcp.Spec.Configuration),
		InternalRegistryHostName:     config.DefaultImageRegistryHostname,
		ExternalRegistryHostNames:    externalRegistryHostNames(hcp.Spec.Configuration),
		DefaultNodeSelector:          defaultNodeSelector(hcp.Spec.Configuration),
		AdvertiseAddress:             util.GetAdvertiseAddress(hcp, config.DefaultAdvertiseIPv4Address, config.DefaultAdvertiseIPv6Address),
		ServiceAccountIssuerURL:      serviceAccountIssuerURL(hcp),
		FeatureGates:                 config.FeatureGates(hcp.Spec.Configuration.GetFeatureGateSelection()),
		NodePortRange:                serviceNodePortRange(hcp.Spec.Configuration),
		ConsolePublicURL:             fmt.Sprintf("https://console-openshift-console.%s", dns.Spec.BaseDomain),
		DisableProfiling:             util.StringListContains(hcp.Annotations[hyperv1.DisableProfilingAnnotation], manifests.KASDeployment("").Name),
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		kasConfig.CloudProvider = aws.Provider
	case hyperv1.AzurePlatform:
		kasConfig.CloudProvider = azure.Provider
		// kasConfig.CloudProviderConfigRef = &corev1.LocalObjectReference{Name: manifests.AzureProviderConfigWithCredentials("").Name}
	}

	switch hcp.Spec.Etcd.ManagementType {
	case hyperv1.Unmanaged:
		if hcp.Spec.Etcd.Unmanaged != nil {
			kasConfig.EtcdURL = hcp.Spec.Etcd.Unmanaged.Endpoint
		}
	case hyperv1.Managed:
		kasConfig.EtcdURL = fmt.Sprintf("https://etcd-client.%s.svc:2379", hcp.Namespace)
	default:
		kasConfig.EtcdURL = config.DefaultEtcdURL
	}

	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		kasConfig.AuditWebhookEnabled = true
	}

	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		kasConfig.APIServerSTSDirectives = "max-age=31536000"
	} else {
		kasConfig.APIServerSTSDirectives = "max-age=31536000,includeSubDomains,preload"
	}

	if hcp.Spec.Configuration != nil {
		kasConfig.Authentication = hcp.Spec.Configuration.Authentication
	}

	kasConfig.MaxRequestsInflight = fmt.Sprint(defaultMaxRequestsInflight)
	if reqInflight := hcp.Annotations[hyperv1.KubeAPIServerMaximumRequestsInFlight]; reqInflight != "" {
		kasConfig.MaxRequestsInflight = reqInflight
	}

	kasConfig.MaxMutatingRequestsInflight = fmt.Sprint(defaultMaxMutatingRequestsInflight)
	if mutatingReqInflight := hcp.Annotations[hyperv1.KubeAPIServerMaximumMutatingRequestsInFlight]; mutatingReqInflight != "" {
		kasConfig.MaxMutatingRequestsInflight = mutatingReqInflight
	}

	return kasConfig
}

func tlsSecurityProfile(configuration *hyperv1.ClusterConfiguration) *configv1.TLSSecurityProfile {
	if configuration != nil && configuration.APIServer != nil {
		return configuration.APIServer.TLSSecurityProfile
	}
	return &configv1.TLSSecurityProfile{
		Type:         configv1.TLSProfileIntermediateType,
		Intermediate: &configv1.IntermediateTLSProfile{},
	}
}

func externalIPConfig(configuration *hyperv1.ClusterConfiguration) *configv1.ExternalIPConfig {
	if configuration != nil && configuration.Network != nil {
		return configuration.Network.ExternalIP
	} else {
		return nil
	}
}

func additionalCORSAllowedOrigins(configuration *hyperv1.ClusterConfiguration) []string {
	if configuration != nil && configuration.APIServer != nil {
		return configuration.APIServer.AdditionalCORSAllowedOrigins
	}
	return []string{}
}

func externalRegistryHostNames(configuration *hyperv1.ClusterConfiguration) []string {
	if configuration != nil && configuration.Image != nil {
		return configuration.Image.ExternalRegistryHostnames
	} else {
		return []string{}
	}
}

func defaultNodeSelector(configuration *hyperv1.ClusterConfiguration) string {
	if configuration != nil && configuration.Scheduler != nil {
		return configuration.Scheduler.DefaultNodeSelector
	} else {
		return ""
	}
}

func serviceAccountIssuerURL(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.IssuerURL != "" {
		return hcp.Spec.IssuerURL
	} else {
		return config.DefaultServiceAccountIssuer
	}
}

func serviceNodePortRange(configuration *hyperv1.ClusterConfiguration) string {
	if configuration != nil && configuration.Network != nil && len(configuration.Network.ServiceNodePortRange) > 0 {
		return configuration.Network.ServiceNodePortRange
	} else {
		return config.DefaultServiceNodePortRange
	}
}

type kmsImages struct {
	IBMCloudKMS      string
	AWSKMS           string
	AzureKMS         string
	TokenMinterImage string
}

func newKMSImages(hcp *hyperv1.HostedControlPlane) kmsImages {
	images := kmsImages{
		AWSKMS:           "aws-kms-encryption-provider",
		AzureKMS:         "azure-kms-encryption-provider",
		TokenMinterImage: "token-minter",
	}

	if image, ok := hcp.Annotations[hyperv1.AWSKMSProviderImage]; ok {
		images.AWSKMS = image
	}
	if image, ok := hcp.Annotations[hyperv1.IBMCloudKMSProviderImage]; ok {
		images.IBMCloudKMS = image
	}

	return images
}
