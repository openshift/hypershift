package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

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
	VPN                   string `json:"vpn"`
}

type KubeAPIServerParams struct {
	APIServer           configv1.APIServer           `json:"apiServer"`
	Authentication      configv1.Authentication      `json:"authentication"`
	FeatureGate         configv1.FeatureGate         `json:"featureGate"`
	Network             configv1.Network             `json:"network"`
	OAuth               configv1.OAuth               `json:"oauth"`
	Image               configv1.Image               `json:"image"`
	Scheduler           configv1.Scheduler           `json:"scheduler"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`

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

func NewKubeAPIServerParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalOAuthAddress string, externalOAuthPort int32) *KubeAPIServerParams {
	params := &KubeAPIServerParams{
		APIServer: configv1.APIServer{
			Spec: configv1.APIServerSpec{
				ServingCerts: configv1.APIServerServingCerts{
					NamedCertificates: []configv1.APIServerNamedServingCert{},
				},
				ClientCA: configv1.ConfigMapNameReference{
					Name: "",
				},
				AdditionalCORSAllowedOrigins: []string{},
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type:         configv1.TLSProfileIntermediateType,
					Intermediate: &configv1.IntermediateTLSProfile{},
				},
				Audit: configv1.Audit{
					Profile: configv1.AuditProfileDefaultType,
				},
			},
		},
		Authentication: configv1.Authentication{
			Spec: configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: manifests.KASOAuthMetadata(hcp.Namespace).Name,
				},
				WebhookTokenAuthenticator: nil,
				ServiceAccountIssuer:      hcp.Spec.IssuerURL,
			},
		},
		FeatureGate: configv1.FeatureGate{
			Spec: configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet:      configv1.Default,
					CustomNoUpgrade: nil,
				},
			},
		},
		Network: config.Network(hcp),
		OAuth: configv1.OAuth{
			Spec: configv1.OAuthSpec{
				TokenConfig: configv1.TokenConfig{
					AccessTokenInactivityTimeout: nil, // Use default
				},
			},
		},
		Image: configv1.Image{
			Spec: configv1.ImageSpec{
				ExternalRegistryHostnames:  []string{},
				AllowedRegistriesForImport: []configv1.RegistryLocation{},
			},
			Status: configv1.ImageStatus{
				InternalRegistryHostname: config.DefaultImageRegistryHostname,
			},
		},
		Scheduler: configv1.Scheduler{
			Spec: configv1.SchedulerSpec{
				DefaultNodeSelector: "",
			},
		},
		AdvertiseAddress:     config.DefaultAdvertiseAddress,
		ExternalAddress:      hcp.Status.ControlPlaneEndpoint.Host,
		ExternalPort:         hcp.Status.ControlPlaneEndpoint.Port,
		ExternalOAuthAddress: externalOAuthAddress,
		ExternalOAuthPort:    externalOAuthPort,
		EtcdURL:              config.DefaultEtcdURL,
		APIServerPort:        config.DefaultAPIServerPort,

		Images: KubeAPIServerImages{
			HyperKube:             images["hyperkube"],
			CLI:                   images["cli"],
			ClusterConfigOperator: images["cluster-config-operator"],
			VPN:                   images["vpn"],
		},
	}
	unprivilegedSecurityContext := corev1.SecurityContext{
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{
				"MKNOD",
				"NET_ADMIN",
			},
		},
		RunAsUser: pointer.Int64Ptr(int64(1001)),
	}
	params.SecurityContexts = config.SecurityContextSpec{
		kasContainerBootstrap().Name:      unprivilegedSecurityContext,
		kasContainerApplyBootstrap().Name: unprivilegedSecurityContext,
		kasContainerMain().Name:           unprivilegedSecurityContext,
		kasContainerVPNClient().Name: {
			Privileged: pointer.BoolPtr(true),
		},
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
	return p.APIServer.Spec.ServingCerts.NamedCertificates
}

func (p *KubeAPIServerParams) AuditPolicyProfile() configv1.AuditProfileType {
	return p.APIServer.Spec.Audit.Profile
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
	return p.Network.Spec.ExternalIP
}

func (p *KubeAPIServerParams) ClusterNetwork() string {
	return config.ClusterCIDR(&p.Network)
}

func (p *KubeAPIServerParams) ServiceNetwork() string {
	return config.ServiceCIDR(&p.Network)
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
	return p.APIServer.Spec.TLSSecurityProfile
}

func (p *KubeAPIServerParams) AdditionalCORSAllowedOrigins() []string {
	return p.APIServer.Spec.AdditionalCORSAllowedOrigins
}

func (p *KubeAPIServerParams) InternalRegistryHostName() string {
	return p.Image.Status.InternalRegistryHostname
}

func (p *KubeAPIServerParams) ExternalRegistryHostNames() []string {
	return p.Image.Spec.ExternalRegistryHostnames
}

func (p *KubeAPIServerParams) DefaultNodeSelector() string {
	return p.Scheduler.Spec.DefaultNodeSelector
}

func (p *KubeAPIServerParams) ServiceAccountIssuerURL() string {
	return p.Authentication.Spec.ServiceAccountIssuer
}

func (p *KubeAPIServerParams) FeatureGates() []string {
	return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
}

func (p *KubeAPIServerParams) ServiceNodePortRange() string {
	return p.Network.Spec.ServiceNodePortRange
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
