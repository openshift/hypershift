package oapi

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	DefaultPriorityClass = "system-node-critical"
)

type OpenShiftAPIServerParams struct {
	APIServer configv1.APIServer `json:"apiServer"`
	Ingress   configv1.Ingress   `json:"ingress"`
	EtcdURL   string             `json:"etcdURL"`

	OpenShiftAPIServerDeploymentConfig      config.DeploymentConfig `json:"openshiftAPIServerDeploymentConfig,inline"`
	OpenShiftOAuthAPIServerDeploymentConfig config.DeploymentConfig `json:"openshiftOAuthAPIServerDeploymentConfig,inline"`
	config.OwnerRef                         `json:",inline"`
	OpenShiftAPIServerImage                 string `json:"openshiftAPIServerImage"`
	OAuthAPIServerImage                     string `json:"oauthAPIServerImage"`
}

type OAuthDeploymentParams struct {
	Image            string
	EtcdURL          string
	MinTLSVersion    string
	CipherSuites     []string
	DeploymentConfig config.DeploymentConfig
}

func NewOpenShiftAPIServerParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *OpenShiftAPIServerParams {
	params := &OpenShiftAPIServerParams{
		OpenShiftAPIServerImage: images["openshift-apiserver"],
		OAuthAPIServerImage:     images["oauth-apiserver"],
		EtcdURL:                 config.DefaultEtcdURL,
		APIServer: configv1.APIServer{
			Spec: configv1.APIServerSpec{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type:         configv1.TLSProfileIntermediateType,
					Intermediate: &configv1.IntermediateTLSProfile{},
				},
			},
		},
		Ingress: configv1.Ingress{
			Spec: configv1.IngressSpec{
				Domain: fmt.Sprintf("apps.%s.%s", hcp.Name, hcp.Spec.DNS.BaseDomain),
			},
		},
	}
	params.OpenShiftAPIServerDeploymentConfig = config.DeploymentConfig{
		AdditionalLabels: map[string]string{},
		Scheduling: config.Scheduling{
			PriorityClass: DefaultPriorityClass,
		},
		LivenessProbes: config.LivenessProbes{
			oasContainerMain().Name: {
				Handler: corev1.Handler{
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
				Handler: corev1.Handler{
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
	params.OpenShiftOAuthAPIServerDeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: DefaultPriorityClass,
		},
		LivenessProbes: config.LivenessProbes{
			oauthContainerMain().Name: {
				Handler: corev1.Handler{
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
				Handler: corev1.Handler{
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

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.OpenShiftAPIServerDeploymentConfig.Replicas = 3
		params.OpenShiftOAuthAPIServerDeploymentConfig.Replicas = 3
	default:
		params.OpenShiftAPIServerDeploymentConfig.Replicas = 1
		params.OpenShiftOAuthAPIServerDeploymentConfig.Replicas = 1
	}
	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *OpenShiftAPIServerParams) MinTLSVersion() string {
	return config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile)
}

func (p *OpenShiftAPIServerParams) CipherSuites() []string {
	return config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile)
}

func (p *OpenShiftAPIServerParams) IngressDomain() string {
	return p.Ingress.Spec.Domain
}

func (p *OpenShiftAPIServerParams) OAuthAPIServerDeploymentParams() *OAuthDeploymentParams {
	return &OAuthDeploymentParams{
		Image:            p.OAuthAPIServerImage,
		EtcdURL:          p.EtcdURL,
		DeploymentConfig: p.OpenShiftOAuthAPIServerDeploymentConfig,
		MinTLSVersion:    p.MinTLSVersion(),
		CipherSuites:     p.CipherSuites(),
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
