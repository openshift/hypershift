package oauth

import (
	"encoding/json"
	"fmt"
	osinv1 "github.com/openshift/api/osin/v1"
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
	// OauthConfigOverrides contains a mapping from provider name to the config overrides specified for the provider.
	// The only supported use case of using this is for the IBMCloud IAM OIDC provider.
	OauthConfigOverrides map[string]*ConfigOverride
}

type OAuthConfigParams struct {
	ExternalHost             string
	ExternalPort             int32
	ServingCert              *corev1.Secret
	CipherSuites             []string
	MinTLSVersion            string
	IdentityProviders        []configv1.IdentityProvider
	AccessTokenMaxAgeSeconds int32
	// OauthConfigOverrides contains a mapping from provider name to the config overrides specified for the provider.
	// The only supported use case of using this is for the IBMCloud IAM OIDC provider.
	OauthConfigOverrides map[string]*ConfigOverride
}

// ConfigOverride defines the oauth parameters that can be overriden in special use cases. The only supported
// use case for this currently is the IBMCloud IAM OIDC provider. These parameters are necessary since the public
// OpenID api does not support some of the customizations used in the IBMCloud IAM OIDC provider. This can be removed
// if the public API is adjusted to allow specifying these customizations.
type ConfigOverride struct {
	URLs      osinv1.OpenIDURLs   `json:"urls"`
	Claims    osinv1.OpenIDClaims `json:"claims"`
	Login     bool                `json:"login"`
	Challenge bool                `json:"challenge"`
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
	if hcp.Annotations != nil {
		if configOverride, ok := hcp.Annotations[hyperv1.IdentityProviderOverridesAnnotation]; ok {
			p.OauthConfigOverrides = map[string]*ConfigOverride{}
			err := json.Unmarshal([]byte(configOverride), &p.OauthConfigOverrides)
			if err != nil {
				//TODO: handle error
				fmt.Println(err)
			}
		}
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
		OauthConfigOverrides:     p.OauthConfigOverrides,
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
