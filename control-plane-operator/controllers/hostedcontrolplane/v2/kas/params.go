package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	KonnectivityServerLocalPort = 8090

	defaultMaxRequestsInflight         = 3000
	defaultMaxMutatingRequestsInflight = 1000
	defaultGoAwayChance                = 0.001
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
	EtcdShardOverrides           map[string]string // maps resource prefix to URL
	FeatureGates                 []string
	NodePortRange                string
	AuditWebhookEnabled          bool
	ConsolePublicURL             string
	DisableProfiling             bool
	APIServerSTSDirectives       string
	Authentication               *configv1.AuthenticationSpec
	MaxRequestsInflight          string
	MaxMutatingRequestsInflight  string
	GoAwayChance                 string
}

func NewConfigParams(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
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
		ExternalRegistryHostNames:    externalRegistryHostNames(hcp.Spec.Configuration),
		DefaultNodeSelector:          defaultNodeSelector(hcp.Spec.Configuration),
		AdvertiseAddress:             util.GetAdvertiseAddress(hcp, config.DefaultAdvertiseIPv4Address, config.DefaultAdvertiseIPv6Address),
		ServiceAccountIssuerURL:      serviceAccountIssuerURL(hcp),
		FeatureGates:                 featureGates,
		NodePortRange:                serviceNodePortRange(hcp.Spec.Configuration),
		ConsolePublicURL:             fmt.Sprintf("https://console-openshift-console.%s", dns.Spec.BaseDomain),
		DisableProfiling:             util.StringListContains(hcp.Annotations[hyperv1.DisableProfilingAnnotation], manifests.KASDeployment("").Name),
	}

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		kasConfig.CloudProvider = aws.Provider
	}

	// Build etcd URLs based on management type
	switch hcp.Spec.Etcd.ManagementType {
	case hyperv1.Unmanaged:
		if hcp.Spec.Etcd.Unmanaged != nil {
			shards := hcp.Spec.Etcd.Unmanaged.EffectiveShards()
			kasConfig.EtcdURL, kasConfig.EtcdShardOverrides = buildUnmanagedEtcdConfig(shards)
		}
	case hyperv1.Managed:
		if hcp.Spec.Etcd.Managed != nil {
			shards := hcp.Spec.Etcd.Managed.EffectiveShards(hcp)
			kasConfig.EtcdURL, kasConfig.EtcdShardOverrides = buildManagedEtcdConfig(shards, hcp.Namespace)
		}
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

	kasConfig.GoAwayChance = fmt.Sprint(defaultGoAwayChance)
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		// There is no point in rebalancing connections if there is only one KAS
		kasConfig.GoAwayChance = "0"
	}
	if goAwayChance := hcp.Annotations[hyperv1.KubeAPIServerGoAwayChance]; goAwayChance != "" {
		kasConfig.GoAwayChance = hcp.Annotations[hyperv1.KubeAPIServerGoAwayChance]
	}

	if maxTokenExpiration := hcp.Annotations[hyperv1.KubeAPIServerServiceAccountTokenMaxExpiration]; maxTokenExpiration != "" {
		kasConfig.ServiceAccountMaxTokenExpiration = maxTokenExpiration
	}

	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		kasConfig.InternalRegistryHostName = config.DefaultImageRegistryHostname
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

// buildManagedEtcdConfig constructs etcd URLs for managed etcd shards
func buildManagedEtcdConfig(shards []hyperv1.ManagedEtcdShardSpec, namespace string) (string, map[string]string) {
	var defaultURL string
	overrides := make(map[string]string)

	for _, shard := range shards {
		// For backward compatibility, use "etcd-client" for the default shard
		// Service naming must match resourceNameForShard() in manifests/etcd.go
		var serviceName string
		if shard.Name == "default" {
			serviceName = "etcd-client"
		} else {
			serviceName = fmt.Sprintf("etcd-client-%s", shard.Name)  // Fixed: was etcd-%s-client
		}
		url := fmt.Sprintf("https://%s.%s.svc:2379", serviceName, namespace)

		for _, prefix := range shard.ResourcePrefixes {
			if prefix == "/" {
				defaultURL = url
			} else {
				// Resource prefixes in API include trailing '#' (e.g., "/events#")
				// --etcd-servers-overrides expects format: /events#https://url
				// So we use prefix as-is (it already has the '#')
				overrides[prefix] = url
			}
		}
	}

	return defaultURL, overrides
}

// buildUnmanagedEtcdConfig constructs etcd URLs for unmanaged etcd shards
func buildUnmanagedEtcdConfig(shards []hyperv1.UnmanagedEtcdShardSpec) (string, map[string]string) {
	var defaultURL string
	overrides := make(map[string]string)

	for _, shard := range shards {
		for _, prefix := range shard.ResourcePrefixes {
			if prefix == "/" {
				defaultURL = shard.Endpoint
			} else {
				overrides[prefix] = shard.Endpoint
			}
		}
	}

	return defaultURL, overrides
}
