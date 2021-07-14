package kas

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

type KubeAPIServerImages struct {
	ClusterConfigOperator string `json:"clusterConfigOperator"`
	CLI                   string `json:"cli"`
	HyperKube             string `json:"hyperKube"`
}

type KubeAPIServerParams struct {
	APIServer           *configv1.APIServer          `json:"apiServer"`
	FeatureGate         *configv1.FeatureGate        `json:"featureGate"`
	Network             *configv1.Network            `json:"network"`
	Image               *configv1.Image              `json:"image"`
	Scheduler           *configv1.Scheduler          `json:"scheduler"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`

	ServiceAccountIssuer string                       `json:"serviceAccountIssuer"`
	ServiceCIDR          string                       `json:"serviceCIDR"`
	PodCIDR              string                       `json:"podCIDR"`
	AdvertiseAddress     string                       `json:"advertiseAddress"`
	ExternalAddress      string                       `json:"externalAddress"`
	ExternalPort         int32                        `json:"externalPort"`
	ExternalOAuthAddress string                       `json:"externalOAuthAddress"`
	ExternalOAuthPort    int32                        `json:"externalOAuthPort"`
	EtcdURL              string                       `json:"etcdAddress"`
	APIServerPort        int32                        `json:"apiServerPort"`
	KubeConfigRef        *hyperv1.KubeconfigSecretRef `json:"kubeConfigRef"`
	AuditWebhookRef      *corev1.LocalObjectReference `json:"auditWebhookRef"`
	config.DeploymentConfig
	config.OwnerRef

	Images KubeAPIServerImages `json:"images"`
}

type KubeAPIServerServiceParams struct {
	APIServerPort  int
	OwnerReference *metav1.OwnerReference
}

func NewKubeAPIServerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig config.GlobalConfig, images map[string]string, externalOAuthAddress string, externalOAuthPort int32) *KubeAPIServerParams {
	params := &KubeAPIServerParams{
		APIServer:            globalConfig.APIServer,
		FeatureGate:          globalConfig.FeatureGate,
		Network:              globalConfig.Network,
		Image:                globalConfig.Image,
		Scheduler:            globalConfig.Scheduler,
		AdvertiseAddress:     config.DefaultAdvertiseAddress,
		ExternalAddress:      hcp.Status.ControlPlaneEndpoint.Host,
		ExternalPort:         hcp.Status.ControlPlaneEndpoint.Port,
		ExternalOAuthAddress: externalOAuthAddress,
		ExternalOAuthPort:    externalOAuthPort,
		APIServerPort:        config.DefaultAPIServerPort,
		ServiceAccountIssuer: hcp.Spec.IssuerURL,
		ServiceCIDR:          hcp.Spec.ServiceCIDR,
		PodCIDR:              hcp.Spec.PodCIDR,

		Images: KubeAPIServerImages{
			HyperKube:             images["hyperkube"],
			CLI:                   images["cli"],
			ClusterConfigOperator: images["cluster-config-operator"],
		},
	}
	switch hcp.Spec.Etcd.ManagementType {
	case hyperv1.Unmanaged:
		params.EtcdURL = hcp.Spec.Etcd.Unmanaged.Endpoint
	case hyperv1.Managed:
		params.EtcdURL = config.DefaultEtcdURL
	default:
		params.EtcdURL = config.DefaultEtcdURL
	}
	params.AdditionalLabels = map[string]string{}
	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.LivenessProbes = config.LivenessProbes{
		kasContainerMain().Name: {
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.APIServerPort)),
					Path:   "livez",
				},
			},
			InitialDelaySeconds: 45,
			TimeoutSeconds:      10,
		},
	}
	params.ReadinessProbes = config.ReadinessProbes{
		kasContainerMain().Name: {
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.APIServerPort)),
					Path:   "readyz",
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      10,
		},
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		kasContainerBootstrap().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("30m"),
			},
		},
		kasContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("1Gi"),
				corev1.ResourceCPU:    resource.MustParse("265m"),
			},
		},
	}
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.CloudProvider = aws.Provider
		params.CloudProviderConfig = &corev1.LocalObjectReference{Name: manifests.AWSProviderConfig("").Name}
	}
	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		params.AuditWebhookRef = hcp.Spec.AuditWebhook
	}

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
	default:
		params.Replicas = 1
	}
	params.KubeConfigRef = hcp.Spec.KubeConfig
	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *KubeAPIServerParams) NamedCertificates() []configv1.APIServerNamedServingCert {
	if p.APIServer != nil {
		return p.APIServer.Spec.ServingCerts.NamedCertificates
	} else {
		return []configv1.APIServerNamedServingCert{}
	}
}

func (p *KubeAPIServerParams) AuditPolicyProfile() configv1.AuditProfileType {
	if p.APIServer != nil {
		return p.APIServer.Spec.Audit.Profile
	} else {
		return configv1.AuditProfileDefaultType
	}
}

func (p *KubeAPIServerParams) ExternalURL() string {
	return fmt.Sprintf("https://%s:%d", p.ExternalAddress, p.ExternalPort)
}

func (p *KubeAPIServerParams) ExternalKubeconfigKey() string {
	if p.KubeConfigRef == nil {
		return ""
	}
	return p.KubeConfigRef.Key
}

func (p *KubeAPIServerParams) ExternalIPConfig() *configv1.ExternalIPConfig {
	if p.Network != nil {
		return p.Network.Spec.ExternalIP
	} else {
		return nil
	}
}

func (p *KubeAPIServerParams) ClusterNetwork() string {
	return p.PodCIDR
}

func (p *KubeAPIServerParams) ServiceNetwork() string {
	return p.ServiceCIDR
}

func (p *KubeAPIServerParams) ConfigParams() KubeAPIServerConfigParams {
	return KubeAPIServerConfigParams{
		ExternalIPConfig:             p.ExternalIPConfig(),
		ClusterNetwork:               p.ClusterNetwork(),
		ServiceNetwork:               p.ServiceNetwork(),
		NamedCertificates:            p.NamedCertificates(),
		ApiServerPort:                p.APIServerPort,
		TLSSecurityProfile:           p.TLSSecurityProfile(),
		AdditionalCORSAllowedOrigins: p.AdditionalCORSAllowedOrigins(),
		InternalRegistryHostName:     p.InternalRegistryHostName(),
		ExternalRegistryHostNames:    p.ExternalRegistryHostNames(),
		DefaultNodeSelector:          p.DefaultNodeSelector(),
		AdvertiseAddress:             p.AdvertiseAddress,
		ServiceAccountIssuerURL:      p.ServiceAccountIssuerURL(),
		CloudProvider:                p.CloudProvider,
		CloudProviderConfigRef:       p.CloudProviderConfig,
		EtcdURL:                      p.EtcdURL,
		FeatureGates:                 p.FeatureGates(),
		NodePortRange:                p.ServiceNodePortRange(),
		AuditWebhookEnabled:          p.AuditWebhookRef != nil,
	}
}

type KubeAPIServerConfigParams struct {
	ExternalIPConfig             *configv1.ExternalIPConfig
	ClusterNetwork               string
	ServiceNetwork               string
	NamedCertificates            []configv1.APIServerNamedServingCert
	ApiServerPort                int32
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
}

func (p *KubeAPIServerParams) TLSSecurityProfile() *configv1.TLSSecurityProfile {
	if p.APIServer != nil {
		return p.APIServer.Spec.TLSSecurityProfile
	}
	return &configv1.TLSSecurityProfile{
		Type:         configv1.TLSProfileIntermediateType,
		Intermediate: &configv1.IntermediateTLSProfile{},
	}
}

func (p *KubeAPIServerParams) AdditionalCORSAllowedOrigins() []string {
	if p.APIServer != nil {
		return p.APIServer.Spec.AdditionalCORSAllowedOrigins
	}
	return []string{}
}

func (p *KubeAPIServerParams) InternalRegistryHostName() string {
	return config.DefaultImageRegistryHostname
}

func (p *KubeAPIServerParams) ExternalRegistryHostNames() []string {
	if p.Image != nil {
		return p.Image.Spec.ExternalRegistryHostnames
	} else {
		return []string{}
	}
}

func (p *KubeAPIServerParams) DefaultNodeSelector() string {
	if p.Scheduler != nil {
		return p.Scheduler.Spec.DefaultNodeSelector
	} else {
		return ""
	}
}

func (p *KubeAPIServerParams) ServiceAccountIssuerURL() string {
	if p.ServiceAccountIssuer != "" {
		return p.ServiceAccountIssuer
	} else {
		return config.DefaultServiceAccountIssuer
	}
}

func (p *KubeAPIServerParams) FeatureGates() []string {
	if p.FeatureGate != nil {
		return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func (p *KubeAPIServerParams) ServiceNodePortRange() string {
	if p.Network != nil && len(p.Network.Spec.ServiceNodePortRange) > 0 {
		return p.Network.Spec.ServiceNodePortRange
	} else {
		return config.DefaultServiceNodePortRange
	}
}

func externalAddress(endpoint hyperv1.APIEndpoint) string {
	return fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port)
}

func NewKubeAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *KubeAPIServerServiceParams {
	return &KubeAPIServerServiceParams{
		APIServerPort:  config.DefaultAPIServerPort,
		OwnerReference: config.ControllerOwnerRef(hcp),
	}
}
