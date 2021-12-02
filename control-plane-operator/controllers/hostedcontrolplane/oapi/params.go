package oapi

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type OpenShiftAPIServerParams struct {
	APIServer               *configv1.APIServer `json:"apiServer"`
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

func NewOpenShiftAPIServerParams(hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig, images map[string]string, explicitNonRootSecurityContext bool) *OpenShiftAPIServerParams {
	params := &OpenShiftAPIServerParams{
		OpenShiftAPIServerImage: images["openshift-apiserver"],
		OAuthAPIServerImage:     images["oauth-apiserver"],
		ProxyImage:              images["socks5-proxy"],
		APIServer:               globalConfig.APIServer,
		ServiceAccountIssuerURL: hcp.Spec.IssuerURL,
		IngressSubDomain:        config.IngressSubdomain(hcp),
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		Availability:            hcp.Spec.ControllerAvailabilityPolicy,
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
	params.OpenShiftAPIServerDeploymentConfig.SetColocation(hcp)
	params.OpenShiftAPIServerDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.OpenShiftAPIServerDeploymentConfig.SetControlPlaneIsolation(hcp)
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
			oasContainerMain().Name: {
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
			oasContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("200Mi"),
					corev1.ResourceCPU:    resource.MustParse("150m"),
				},
			},
		},
	}
	if explicitNonRootSecurityContext {
		params.OpenShiftAPIServerDeploymentConfig.SecurityContexts = config.SecurityContextSpec{
			oasContainerMain().Name: {
				RunAsUser: k8sutilspointer.Int64Ptr(1001),
			},
			oasKonnectivityProxyContainer().Name: {
				RunAsUser: k8sutilspointer.Int64Ptr(1001),
			},
		}
		params.OpenShiftOAuthAPIServerDeploymentConfig.SecurityContexts = config.SecurityContextSpec{
			oauthContainerMain().Name: {
				RunAsUser: k8sutilspointer.Int64Ptr(1001),
			},
		}
	}
	params.OpenShiftOAuthAPIServerDeploymentConfig.SetColocation(hcp)
	params.OpenShiftOAuthAPIServerDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.OpenShiftOAuthAPIServerDeploymentConfig.SetControlPlaneIsolation(hcp)
	switch hcp.Spec.Etcd.ManagementType {
	case hyperv1.Unmanaged:
		params.EtcdURL = hcp.Spec.Etcd.Unmanaged.Endpoint
	case hyperv1.Managed:
		params.EtcdURL = config.DefaultEtcdURL
	default:
		params.EtcdURL = config.DefaultEtcdURL
	}
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.OpenShiftAPIServerDeploymentConfig.Replicas = 3
		params.OpenShiftOAuthAPIServerDeploymentConfig.Replicas = 3
		params.OpenShiftOAuthAPIServerDeploymentConfig.SetMultizoneSpread(openShiftOAuthAPIServerLabels())
		params.OpenShiftAPIServerDeploymentConfig.SetMultizoneSpread(openShiftAPIServerLabels())
	default:
		params.OpenShiftAPIServerDeploymentConfig.Replicas = 1
		params.OpenShiftOAuthAPIServerDeploymentConfig.Replicas = 1
	}
	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *OpenShiftAPIServerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *OpenShiftAPIServerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile)
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
