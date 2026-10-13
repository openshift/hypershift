package oauth

import (
	"encoding/json"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"

	corev1 "k8s.io/api/core/v1"
)

type OAuthServerParams struct {
	OwnerRef         config.OwnerRef `json:"ownerRef"`
	ExternalHost     string          `json:"externalHost"`
	ExternalPort     int32           `json:"externalPort"`
	ExternalAPIHost  string          `json:"externalAPIHost"`
	ExternalAPIPort  int32           `json:"externalAPIPort"`
	OAuthServerImage string
	OAuth            *configv1.OAuthSpec
	ProxyConfig      *configv1.ProxySpec
	APIServer        *configv1.APIServerSpec `json:"apiServer"`
	// OauthConfigOverrides contains a mapping from provider name to the config overrides specified for the provider.
	// The only supported use case of using this is for the IBMCloud IAM OIDC provider.
	OauthConfigOverrides map[string]*ConfigOverride
	// LoginURLOverride can be used to specify an override for the oauth config login url. The need for this arises
	// when the login a provider uses doesn't conform to the standard login url in hypershift. The only supported use case
	// for this is IBMCloud Red Hat Openshift
	LoginURLOverride        string
	AvailabilityProberImage string `json:"availabilityProberImage"`
	Availability            hyperv1.AvailabilityPolicy
	// ProxyImage is the image that contains the control-plane-operator binary that will
	// be used to run konnectivity-socks5-proxy and konnectivity-https-proxy
	ProxyImage string
	// OAuthNoProxy is a list of hosts or IPs that should not be routed through
	// konnectivity. Currently only used for IBM Cloud specific addresses.
	OAuthNoProxy    []string
	AuditWebhookRef *corev1.LocalObjectReference
}

// ConfigOverride defines the oauth parameters that can be overridden in special use cases. The only supported
// use case for this currently is the IBMCloud IAM OIDC provider. These parameters are necessary since the public
// OpenID api does not support some customizations used in the IBMCloud IAM OIDC provider. This can be removed
// if the public API is adjusted to allow specifying these customizations.
type ConfigOverride struct {
	URLs      osinv1.OpenIDURLs   `json:"urls,omitempty"`
	Claims    osinv1.OpenIDClaims `json:"claims,omitempty"`
	Challenge *bool               `json:"challenge,omitempty"`
}

func NewOAuthServerParams(hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, host string, port int32, setDefaultSecurityContext bool) *OAuthServerParams {
	p := &OAuthServerParams{
		OwnerRef:                config.OwnerRefFrom(hcp),
		ExternalAPIHost:         hcp.Status.ControlPlaneEndpoint.Host,
		ExternalAPIPort:         hcp.Status.ControlPlaneEndpoint.Port,
		ExternalHost:            host,
		ExternalPort:            port,
		OAuthServerImage:        releaseImageProvider.GetImage("oauth-server"),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		Availability:            hcp.Spec.ControllerAvailabilityPolicy,
		ProxyImage:              releaseImageProvider.GetImage("socks5-proxy"),
		OAuthNoProxy:            []string{manifests.KubeAPIServerService("").Name, config.AuditWebhookService},
	}
	if hcp.Spec.Configuration != nil {
		p.APIServer = hcp.Spec.Configuration.APIServer
		p.OAuth = hcp.Spec.Configuration.OAuth
		p.ProxyConfig = hcp.Spec.Configuration.Proxy
	}

	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		p.AuditWebhookRef = hcp.Spec.AuditWebhook
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

	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		p.OAuthNoProxy = append(p.OAuthNoProxy, "iam.cloud.ibm.com", "iam.test.cloud.ibm.com")
	}

	return p
}

func (p *OAuthServerParams) IdentityProviders() []configv1.IdentityProvider {
	if p.OAuth != nil {
		return p.OAuth.IdentityProviders
	}
	return []configv1.IdentityProvider{}
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
