package oauth

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type OAuthServerParams struct {
	OwnerRef                config.OwnerRef `json:"ownerRef"`
	ExternalHost            string          `json:"externalHost"`
	ExternalPort            int32           `json:"externalPort"`
	OAuthServerImage        string
	config.DeploymentConfig `json:",inline"`
	OAuth                   configv1.OAuth     `json:"oauth"`
	APIServer               configv1.APIServer `json:"apiServer"`
}

type OAuthConfigParams struct {
	ExternalHost             string
	ExternalPort             int32
	ServingCert              *corev1.Secret
	CipherSuites             []string
	MinTLSVersion            string
	IdentityProviders        []configv1.IdentityProvider
	AccessTokenMaxAgeSeconds int32
}

func NewOAuthServerParams(hcp *hyperv1.HostedControlPlane, images map[string]string, host string, port int32) *OAuthServerParams {
	p := &OAuthServerParams{
		OwnerRef:         config.OwnerRefFrom(hcp),
		ExternalHost:     host,
		ExternalPort:     port,
		OAuthServerImage: images["oauth-server"],
		OAuth: configv1.OAuth{
			Spec: configv1.OAuthSpec{
				TokenConfig: configv1.TokenConfig{
					AccessTokenMaxAgeSeconds: 86400,
				},
			},
		},
		APIServer: configv1.APIServer{
			Spec: configv1.APIServerSpec{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type:         configv1.TLSProfileIntermediateType,
					Intermediate: &configv1.IntermediateTLSProfile{},
				},
			},
		},
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

func (p *OAuthServerParams) ConfigParams(servingCert *corev1.Secret) *OAuthConfigParams {
	return &OAuthConfigParams{
		ExternalHost:             p.ExternalHost,
		ExternalPort:             p.ExternalPort,
		ServingCert:              servingCert,
		CipherSuites:             config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile),
		MinTLSVersion:            config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile),
		IdentityProviders:        p.OAuth.Spec.IdentityProviders,
		AccessTokenMaxAgeSeconds: p.OAuth.Spec.TokenConfig.AccessTokenMaxAgeSeconds,
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
