package oapi

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type OpenShiftAPIServerParams struct {
	APIServer               *configv1.APIServerSpec `json:"apiServer"`
	IngressSubDomain        string
	EtcdURL                 string `json:"etcdURL"`
	ServiceAccountIssuerURL string `json:"serviceAccountIssuerURL"`

	OpenShiftAPIServerDeploymentConfig      config.DeploymentConfig `json:"openshiftAPIServerDeploymentConfig,inline"`
	OpenShiftOAuthAPIServerDeploymentConfig config.DeploymentConfig `json:"openshiftOAuthAPIServerDeploymentConfig,inline"`
	config.OwnerRef                         `json:",inline"`
	OpenShiftAPIServerImage                 string `json:"openshiftAPIServerImage"`
	OAuthAPIServerImage                     string `json:"oauthAPIServerImage"`
	ProxyImage                              string `json:"haproxyImage"`
	AvailabilityProberImage                 string `json:"availabilityProberImage"`
	Availability                            hyperv1.AvailabilityPolicy
	Ingress                                 *configv1.IngressSpec
	Image                                   *configv1.Image
	Project                                 *configv1.Project
}

type OAuthDeploymentParams struct {
	Image                   string
	EtcdURL                 string
	MinTLSVersion           string
	CipherSuites            []string
	ServiceAccountIssuerURL string
	DeploymentConfig        config.DeploymentConfig
	AvailabilityProberImage string
	Availability            hyperv1.AvailabilityPolicy
	OwnerRef                config.OwnerRef
}

func NewOpenShiftAPIServerParams(hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, images map[string]string, setDefaultSecurityContext bool) *OpenShiftAPIServerParams {
	params := &OpenShiftAPIServerParams{
		OpenShiftAPIServerImage: images["openshift-apiserver"],
		OAuthAPIServerImage:     images["oauth-apiserver"],
		ProxyImage:              images["socks5-proxy"],
		ServiceAccountIssuerURL: hcp.Spec.IssuerURL,
		IngressSubDomain:        globalconfig.IngressDomain(hcp),
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		Availability:            hcp.Spec.ControllerAvailabilityPolicy,
		Image:                   observedConfig.Image,
		Project:                 observedConfig.Project,
	}

	if hcp.Spec.Configuration != nil {
		params.Ingress = hcp.Spec.Configuration.Ingress
		params.APIServer = hcp.Spec.Configuration.APIServer
	}

	params.OpenShiftAPIServerDeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.APICriticalPriorityClass,
		},
		LivenessProbes: config.LivenessProbes{
			oasContainerMain().Name: {
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Scheme: corev1.URISchemeHTTPS,
						Port:   intstr.FromInt(int(OpenShiftAPIServerPort)),
						Path:   "healthz",
					},
				},
				InitialDelaySeconds: 30,
				TimeoutSeconds:      10,
				PeriodSeconds:       10,
				FailureThreshold:    3,
				SuccessThreshold:    1,
			},
		},
		ReadinessProbes: config.ReadinessProbes{
			oasContainerMain().Name: {
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Scheme: corev1.URISchemeHTTPS,
						Port:   intstr.FromInt(int(OpenShiftAPIServerPort)),
						Path:   "healthz",
					},
				},
				TimeoutSeconds:   1,
				PeriodSeconds:    10,
				SuccessThreshold: 1,
				FailureThreshold: 10,
			},
		},
		Resources: map[string]corev1.ResourceRequirements{
			oasContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("200Mi"),
					corev1.ResourceCPU:    resource.MustParse("100m"),
				},
			},
		},
	}
	params.OpenShiftAPIServerDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.OpenShiftAPIServerDeploymentConfig.SetDefaults(hcp, openShiftAPIServerLabels(), nil)

	params.OpenShiftOAuthAPIServerDeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.APICriticalPriorityClass,
		},
		LivenessProbes: config.LivenessProbes{
			oauthContainerMain().Name: {
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Scheme: corev1.URISchemeHTTPS,
						Port:   intstr.FromInt(int(OpenShiftOAuthAPIServerPort)),
						Path:   "healthz",
					},
				},
				InitialDelaySeconds: 30,
				TimeoutSeconds:      1,
				PeriodSeconds:       10,
				FailureThreshold:    3,
				SuccessThreshold:    1,
			},
		},
		ReadinessProbes: config.ReadinessProbes{
			oauthContainerMain().Name: {
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Scheme: corev1.URISchemeHTTPS,
						Port:   intstr.FromInt(int(OpenShiftOAuthAPIServerPort)),
						Path:   "readyz",
					},
				},
				TimeoutSeconds:   1,
				PeriodSeconds:    10,
				SuccessThreshold: 1,
				FailureThreshold: 10,
			},
		},
		Resources: map[string]corev1.ResourceRequirements{
			oauthContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("80Mi"),
					corev1.ResourceCPU:    resource.MustParse("150m"),
				},
			},
		},
	}

	params.OpenShiftAPIServerDeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	params.OpenShiftOAuthAPIServerDeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.OpenShiftOAuthAPIServerDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.OpenShiftOAuthAPIServerDeploymentConfig.SetDefaults(hcp, openShiftOAuthAPIServerLabels(), nil)
	switch hcp.Spec.Etcd.ManagementType {
	case hyperv1.Unmanaged:
		params.EtcdURL = hcp.Spec.Etcd.Unmanaged.Endpoint
	case hyperv1.Managed:
		params.EtcdURL = config.DefaultEtcdURL
	default:
		params.EtcdURL = config.DefaultEtcdURL
	}

	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *OpenShiftAPIServerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *OpenShiftAPIServerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}

func (p *OpenShiftAPIServerParams) IngressDomain() string {
	return p.IngressSubDomain
}

func (p *OpenShiftAPIServerParams) OAuthAPIServerDeploymentParams() *OAuthDeploymentParams {
	return &OAuthDeploymentParams{
		Image:                   p.OAuthAPIServerImage,
		EtcdURL:                 p.EtcdURL,
		ServiceAccountIssuerURL: p.ServiceAccountIssuerURL,
		DeploymentConfig:        p.OpenShiftOAuthAPIServerDeploymentConfig,
		MinTLSVersion:           p.MinTLSVersion(),
		CipherSuites:            p.CipherSuites(),
		AvailabilityProberImage: p.AvailabilityProberImage,
		Availability:            p.Availability,
		OwnerRef:                p.OwnerRef,
	}
}

type OpenShiftAPIServerServiceParams struct {
	OwnerRef config.OwnerRef `json:"ownerRef"`
}

func NewOpenShiftAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *OpenShiftAPIServerServiceParams {
	return &OpenShiftAPIServerServiceParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
}
