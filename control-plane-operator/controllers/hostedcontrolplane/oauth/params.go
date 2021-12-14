package oauth

import (
	"encoding/json"
	"strings"

	"k8s.io/apimachinery/pkg/util/intstr"

	osinv1 "github.com/openshift/api/osin/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sutilspointer "k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
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
	// OauthConfigOverrides contains a mapping from provider name to the config overrides specified for the provider.
	// The only supported use case of using this is for the IBMCloud IAM OIDC provider.
	OauthConfigOverrides map[string]*ConfigOverride
	// LoginURLOverride can be used to specify an override for the oauth config login url. The need for this arises
	// when the login a provider uses doesn't conform to the standard login url in hypershift. The only supported use case
	// for this is IBMCloud Red Hat Openshift
	LoginURLOverride        string
	AvailabilityProberImage string `json:"availabilityProberImage"`
	Availability            hyperv1.AvailabilityPolicy
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
	// OauthConfigOverrides contains a mapping from provider name to the config overrides specified for the provider.
	// The only supported use case of using this is for the IBMCloud IAM OIDC provider.
	OauthConfigOverrides map[string]*ConfigOverride
	// LoginURLOverride can be used to specify an override for the oauth config login url. The need for this arises
	// when the login a provider uses doesn't conform to the standard login url in hypershift. The only supported use case
	// for this is IBMCloud Red Hat Openshift
	LoginURLOverride string
}

// ConfigOverride defines the oauth parameters that can be overriden in special use cases. The only supported
// use case for this currently is the IBMCloud IAM OIDC provider. These parameters are necessary since the public
// OpenID api does not support some of the customizations used in the IBMCloud IAM OIDC provider. This can be removed
// if the public API is adjusted to allow specifying these customizations.
type ConfigOverride struct {
	URLs   osinv1.OpenIDURLs   `json:"urls,omitempty"`
	Claims osinv1.OpenIDClaims `json:"claims,omitempty"`
}

func NewOAuthServerParams(hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig, images map[string]string, host string, port int32, explicitNonRootSecurityContext bool) *OAuthServerParams {
	p := &OAuthServerParams{
		OwnerRef:                config.OwnerRefFrom(hcp),
		ExternalAPIHost:         hcp.Status.ControlPlaneEndpoint.Host,
		ExternalAPIPort:         hcp.Status.ControlPlaneEndpoint.Port,
		ExternalHost:            host,
		ExternalPort:            port,
		OAuthServerImage:        images["oauth-server"],
		OAuth:                   globalConfig.OAuth,
		APIServer:               globalConfig.APIServer,
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		Availability:            hcp.Spec.ControllerAvailabilityPolicy,
	}
	p.Scheduling = config.Scheduling{
		PriorityClass: config.APICriticalPriorityClass,
	}
	p.Resources = map[string]corev1.ResourceRequirements{
		oauthContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("150Mi"),
				corev1.ResourceCPU:    resource.MustParse("25m"),
			},
		},
	}
	p.LivenessProbes = config.LivenessProbes{
		oauthContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(OAuthServerPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			TimeoutSeconds:      10,
			PeriodSeconds:       60,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
	}
	p.ReadinessProbes = config.ReadinessProbes{
		oauthContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(OAuthServerPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      10,
			PeriodSeconds:       30,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
	}
	p.DeploymentConfig.SetColocation(hcp)
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.DeploymentConfig.SetMultizoneSpread(oauthServerLabels)
		p.Replicas = 3
	default:
		p.Replicas = 1
	}
	p.OauthConfigOverrides = map[string]*ConfigOverride{}
	for annotationKey, annotationValue := range hcp.Annotations {
		if strings.HasPrefix(annotationKey, hyperv1.IdentityProviderOverridesAnnotationPrefix) {
			tokenizedString := strings.Split(annotationKey, hyperv1.IdentityProviderOverridesAnnotationPrefix)
			if len(tokenizedString) == 2 {
				identityProvider := tokenizedString[1]
				providerConfigOverride := &ConfigOverride{}
				err := json.Unmarshal([]byte(annotationValue), providerConfigOverride)
				if err == nil {
					p.OauthConfigOverrides[identityProvider] = providerConfigOverride
				}
			}
		} else if annotationKey == hyperv1.OauthLoginURLOverrideAnnotation {
			p.LoginURLOverride = annotationValue
		}
	}

	if explicitNonRootSecurityContext {
		// iterate over resources and set security context to all the containers
		securityContextsObj := make(config.SecurityContextSpec)
		for containerName := range p.DeploymentConfig.Resources {
			securityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		p.DeploymentConfig.SecurityContexts = securityContextsObj
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
		OauthConfigOverrides:     p.OauthConfigOverrides,
		LoginURLOverride:         p.LoginURLOverride,
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
