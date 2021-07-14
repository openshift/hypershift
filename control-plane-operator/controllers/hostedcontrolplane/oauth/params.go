package oauth

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	defaultAccessTokenMaxAgeSeconds int32 = 86400
)

type OAuthServerParams struct {
	OwnerRef                config.OwnerRef `json:"ownerRef"`
	ExternalHost            string          `json:"externalHost"`
	ExternalPort            int32           `json:"externalPort"`
	ExternalAPIHost         string          `json:"externalAPIHost"`
	ExternalAPIPort         int32           `json:"externalAPIPort"`
	OAuthServerImage        string
	config.DeploymentConfig `json:",inline"`
	OAuth                   *configv1.OAuth     `json:"oauth"`
	APIServer               *configv1.APIServer `json:"apiServer"`
}

type OAuthConfigParams struct {
	ExternalAPIHost          string
	ExternalAPIPort          int32
	ExternalHost             string
	ExternalPort             int32
	ServingCert              *corev1.Secret
	CipherSuites             []string
	MinTLSVersion            string
	IdentityProviders        []configv1.IdentityProvider
	AccessTokenMaxAgeSeconds int32
}

func NewOAuthServerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig config.GlobalConfig, images map[string]string, host string, port int32) *OAuthServerParams {
	p := &OAuthServerParams{
		OwnerRef:         config.OwnerRefFrom(hcp),
		ExternalAPIHost:  hcp.Status.ControlPlaneEndpoint.Host,
		ExternalAPIPort:  hcp.Status.ControlPlaneEndpoint.Port,
		ExternalHost:     host,
		ExternalPort:     port,
		OAuthServerImage: images["oauth-server"],
		OAuth:            globalConfig.OAuth,
		APIServer:        globalConfig.APIServer,
	}
	p.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	p.Resources = map[string]corev1.ResourceRequirements{
		oauthContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("150Mi"),
				corev1.ResourceCPU:    resource.MustParse("25m"),
			},
		},
	}
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.Replicas = 3
	default:
		p.Replicas = 1
	}
	return p
}

func (p *OAuthServerParams) IdentityProviders() []configv1.IdentityProvider {
	if p.OAuth != nil {
		return p.OAuth.Spec.IdentityProviders
	}
	return []configv1.IdentityProvider{}
}

func (p *OAuthServerParams) AccessTokenMaxAgeSeconds() int32 {
	if p.OAuth != nil && p.OAuth.Spec.TokenConfig.AccessTokenMaxAgeSeconds > 0 {
		return p.OAuth.Spec.TokenConfig.AccessTokenMaxAgeSeconds
	}
	return defaultAccessTokenMaxAgeSeconds
}

func (p *OAuthServerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *OAuthServerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}

func (p *OAuthServerParams) ConfigParams(servingCert *corev1.Secret) *OAuthConfigParams {
	return &OAuthConfigParams{
		ExternalHost:             p.ExternalHost,
		ExternalPort:             p.ExternalPort,
		ExternalAPIHost:          p.ExternalAPIHost,
		ExternalAPIPort:          p.ExternalAPIPort,
		ServingCert:              servingCert,
		CipherSuites:             p.CipherSuites(),
		MinTLSVersion:            p.MinTLSVersion(),
		IdentityProviders:        p.IdentityProviders(),
		AccessTokenMaxAgeSeconds: p.AccessTokenMaxAgeSeconds(),
	}
}

type OAuthServiceParams struct {
	OAuth    *configv1.OAuth `json:"oauth"`
	OwnerRef config.OwnerRef `json:"ownerRef"`
}

func NewOAuthServiceParams(hcp *hyperv1.HostedControlPlane) *OAuthServiceParams {
	return &OAuthServiceParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
}
