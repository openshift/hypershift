package oapi

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type OpenShiftAPIServerParams struct {
	APIServer               *configv1.APIServerSpec
	Proxy                   *configv1.ProxySpec
	IngressSubDomain        string
	EtcdURL                 string
	ServiceAccountIssuerURL string

	OpenShiftAPIServerDeploymentConfig      config.DeploymentConfig
	OpenShiftOAuthAPIServerDeploymentConfig config.DeploymentConfig
	config.OwnerRef
	OpenShiftAPIServerImage string
	OAuthAPIServerImage     string
	ProxyImage              string
	AvailabilityProberImage string
	Availability            hyperv1.AvailabilityPolicy
	Ingress                 *configv1.IngressSpec
	Image                   *configv1.ImageSpec
	Project                 *configv1.Project
	AuditEnabled            bool
	AuditWebhookRef         *corev1.LocalObjectReference
	InternalOAuthDisable    bool
}

type OAuthDeploymentParams struct {
	Image                        string
	EtcdURL                      string
	MinTLSVersion                string
	CipherSuites                 []string
	ServiceAccountIssuerURL      string
	DeploymentConfig             config.DeploymentConfig
	AvailabilityProberImage      string
	Availability                 hyperv1.AvailabilityPolicy
	OwnerRef                     config.OwnerRef
	AuditEnabled                 bool
	AuditWebhookRef              *corev1.LocalObjectReference
	AccessTokenInactivityTimeout *metav1.Duration
}

func NewOpenShiftAPIServerParams(hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) *OpenShiftAPIServerParams {
	params := &OpenShiftAPIServerParams{
		OpenShiftAPIServerImage: releaseImageProvider.GetImage("openshift-apiserver"),
		OAuthAPIServerImage:     releaseImageProvider.GetImage("oauth-apiserver"),
		ProxyImage:              releaseImageProvider.GetImage(util.CPOImageName),
		ServiceAccountIssuerURL: hcp.Spec.IssuerURL,
		IngressSubDomain:        globalconfig.IngressDomain(hcp),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		Availability:            hcp.Spec.ControllerAvailabilityPolicy,
		Project:                 observedConfig.Project,
		AuditEnabled:            true,
		InternalOAuthDisable:    !util.HCPOAuthEnabled(hcp),
	}

	if hcp.Spec.Configuration != nil {
		params.Ingress = hcp.Spec.Configuration.Ingress
		params.APIServer = hcp.Spec.Configuration.APIServer
		params.Image = hcp.Spec.Configuration.Image
		params.Proxy = hcp.Spec.Configuration.Proxy
	}

	if params.APIServer != nil {
		params.AuditEnabled = params.APIServer.Audit.Profile != configv1.NoneAuditProfileType
	}

	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		params.AuditWebhookRef = hcp.Spec.AuditWebhook
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
	if hcp.Annotations[hyperv1.APICriticalPriorityClass] != "" {
		params.OpenShiftAPIServerDeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.APICriticalPriorityClass]
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
	if hcp.Annotations[hyperv1.APICriticalPriorityClass] != "" {
		params.OpenShiftOAuthAPIServerDeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.APICriticalPriorityClass]
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

func (p *OpenShiftAPIServerParams) AuditPolicyConfig() configv1.Audit {
	if p.APIServer != nil && p.APIServer.Audit.Profile != "" {
		return p.APIServer.Audit
	} else {
		return configv1.Audit{
			Profile: configv1.DefaultAuditProfileType,
		}
	}
}

func (p *OpenShiftAPIServerParams) OAuthAPIServerDeploymentParams(hcp *hyperv1.HostedControlPlane) *OAuthDeploymentParams {
	params := &OAuthDeploymentParams{
		Image:                   p.OAuthAPIServerImage,
		EtcdURL:                 p.EtcdURL,
		ServiceAccountIssuerURL: p.ServiceAccountIssuerURL,
		DeploymentConfig:        p.OpenShiftOAuthAPIServerDeploymentConfig,
		MinTLSVersion:           p.MinTLSVersion(),
		CipherSuites:            p.CipherSuites(),
		AvailabilityProberImage: p.AvailabilityProberImage,
		Availability:            p.Availability,
		OwnerRef:                p.OwnerRef,
		AuditEnabled:            p.AuditEnabled,
	}

	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		params.AuditWebhookRef = hcp.Spec.AuditWebhook
	}

	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.OAuth != nil && hcp.Spec.Configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout != nil {
		params.AccessTokenInactivityTimeout = hcp.Spec.Configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout
	}

	return params
}

type OpenShiftAPIServerServiceParams struct {
	OwnerRef config.OwnerRef `json:"ownerRef"`
}

func NewOpenShiftAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *OpenShiftAPIServerServiceParams {
	return &OpenShiftAPIServerServiceParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
}
