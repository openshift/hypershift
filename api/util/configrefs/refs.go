package configrefs

import (
	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/sets"
)

// ClusterConfiguration is an interface for the ClusterConfiguration type in the API
// It is needed to avoid a circular import reference, given that this package is
// used by the conversion code in the API package.
type ClusterConfiguration interface {
	GetAPIServer() *configv1.APIServerSpec
	GetAuthentication() *configv1.AuthenticationSpec
	GetFeatureGate() *configv1.FeatureGateSpec
	GetImage() *configv1.ImageSpec
	GetIngress() *configv1.IngressSpec
	GetNetwork() *configv1.NetworkSpec
	GetOAuth() *configv1.OAuthSpec
	GetScheduler() *configv1.SchedulerSpec
	GetProxy() *configv1.ProxySpec
}

func SecretRefs(cfg ClusterConfiguration) []string {
	result := sets.NewString()
	result = result.Union(apiServerSecretRefs(cfg.GetAPIServer()))
	result = result.Union(authenticationSecretRefs(cfg.GetAuthentication()))
	result = result.Union(ingressSecretRefs(cfg.GetIngress()))
	result = result.Union(oauthSecretRefs(cfg.GetOAuth()))
	return result.List()
}

func ConfigMapRefs(cfg ClusterConfiguration) []string {
	result := sets.NewString()
	result = result.Union(apiServerConfigMapRefs(cfg.GetAPIServer()))
	result = result.Union(authenticationConfigMapRefs(cfg.GetAuthentication()))
	result = result.Union(imageConfigMapRefs(cfg.GetImage()))
	result = result.Union(oauthConfigMapRefs(cfg.GetOAuth()))
	result = result.Union(proxyConfigMapRefs(cfg.GetProxy()))
	result = result.Union(schedulerConfigMapRefs(cfg.GetScheduler()))
	return result.List()
}

func apiServerSecretRefs(spec *configv1.APIServerSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	for _, namedCert := range spec.ServingCerts.NamedCertificates {
		if len(namedCert.ServingCertificate.Name) > 0 {
			refs.Insert(namedCert.ServingCertificate.Name)
		}
	}
	return refs
}

func apiServerConfigMapRefs(spec *configv1.APIServerSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	if len(spec.ClientCA.Name) > 0 {
		refs.Insert(spec.ClientCA.Name)
	}
	return refs
}

func authenticationSecretRefs(spec *configv1.AuthenticationSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	for _, tokenAuth := range spec.WebhookTokenAuthenticators {
		if len(tokenAuth.KubeConfig.Name) > 0 {
			refs.Insert(tokenAuth.KubeConfig.Name)
		}
	}
	if spec.WebhookTokenAuthenticator != nil {
		if len(spec.WebhookTokenAuthenticator.KubeConfig.Name) > 0 {
			refs.Insert(spec.WebhookTokenAuthenticator.KubeConfig.Name)
		}
	}
	if spec.Type == configv1.AuthenticationTypeOIDC {
		for _, provider := range spec.OIDCProviders {
			for _, client := range provider.OIDCClients {
				if len(client.ClientSecret.Name) > 0 {
					refs.Insert(client.ClientSecret.Name)
				}
			}
		}
	}
	return refs
}

func authenticationConfigMapRefs(spec *configv1.AuthenticationSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	if len(spec.OAuthMetadata.Name) > 0 {
		refs.Insert(spec.OAuthMetadata.Name)
	}
	if spec.Type == configv1.AuthenticationTypeOIDC {
		for _, provider := range spec.OIDCProviders {
			if len(provider.Issuer.CertificateAuthority.Name) > 0 {
				refs.Insert(provider.Issuer.CertificateAuthority.Name)
			}
		}
	}
	return refs
}

func ingressSecretRefs(spec *configv1.IngressSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	for _, componentRoute := range spec.ComponentRoutes {
		if len(componentRoute.ServingCertKeyPairSecret.Name) > 0 {
			refs.Insert(componentRoute.ServingCertKeyPairSecret.Name)
		}
	}
	return refs
}

func imageConfigMapRefs(spec *configv1.ImageSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	if len(spec.AdditionalTrustedCA.Name) > 0 {
		refs.Insert(spec.AdditionalTrustedCA.Name)
	}
	return refs
}

func oauthSecretRefs(spec *configv1.OAuthSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	for _, idp := range spec.IdentityProviders {
		switch {
		case idp.BasicAuth != nil:
			if len(idp.BasicAuth.TLSClientCert.Name) > 0 {
				refs.Insert(idp.BasicAuth.TLSClientCert.Name)
			}
			if len(idp.BasicAuth.TLSClientKey.Name) > 0 {
				refs.Insert(idp.BasicAuth.TLSClientKey.Name)
			}
		case idp.GitHub != nil:
			if len(idp.GitHub.ClientSecret.Name) > 0 {
				refs.Insert(idp.GitHub.ClientSecret.Name)
			}
		case idp.GitLab != nil:
			if len(idp.GitLab.ClientSecret.Name) > 0 {
				refs.Insert(idp.GitLab.ClientSecret.Name)
			}
		case idp.Google != nil:
			if len(idp.Google.ClientSecret.Name) > 0 {
				refs.Insert(idp.Google.ClientSecret.Name)
			}
		case idp.HTPasswd != nil:
			if len(idp.HTPasswd.FileData.Name) > 0 {
				refs.Insert(idp.HTPasswd.FileData.Name)
			}
		case idp.Keystone != nil:
			if len(idp.Keystone.TLSClientCert.Name) > 0 {
				refs.Insert(idp.Keystone.TLSClientCert.Name)
			}
			if len(idp.Keystone.TLSClientKey.Name) > 0 {
				refs.Insert(idp.Keystone.TLSClientKey.Name)
			}
		case idp.LDAP != nil:
			if len(idp.LDAP.BindPassword.Name) > 0 {
				refs.Insert(idp.LDAP.BindPassword.Name)
			}
		case idp.OpenID != nil:
			if len(idp.OpenID.ClientSecret.Name) > 0 {
				refs.Insert(idp.OpenID.ClientSecret.Name)
			}
		}
	}
	if len(spec.Templates.Error.Name) > 0 {
		refs.Insert(spec.Templates.Error.Name)
	}
	if len(spec.Templates.Login.Name) > 0 {
		refs.Insert(spec.Templates.Login.Name)
	}
	if len(spec.Templates.ProviderSelection.Name) > 0 {
		refs.Insert(spec.Templates.ProviderSelection.Name)
	}
	return refs
}

func oauthConfigMapRefs(spec *configv1.OAuthSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	for _, idp := range spec.IdentityProviders {
		switch {
		case idp.BasicAuth != nil:
			if len(idp.BasicAuth.CA.Name) > 0 {
				refs.Insert(idp.BasicAuth.CA.Name)
			}
		case idp.GitHub != nil:
			if len(idp.GitHub.CA.Name) > 0 {
				refs.Insert(idp.GitHub.CA.Name)
			}
		case idp.GitLab != nil:
			if len(idp.GitLab.CA.Name) > 0 {
				refs.Insert(idp.GitLab.CA.Name)
			}
		case idp.Keystone != nil:
			if len(idp.Keystone.CA.Name) > 0 {
				refs.Insert(idp.Keystone.CA.Name)
			}
		case idp.LDAP != nil:
			if len(idp.LDAP.CA.Name) > 0 {
				refs.Insert(idp.LDAP.CA.Name)
			}
		case idp.OpenID != nil:
			if len(idp.OpenID.CA.Name) > 0 {
				refs.Insert(idp.OpenID.CA.Name)
			}
		case idp.RequestHeader != nil:
			if len(idp.RequestHeader.ClientCA.Name) > 0 {
				refs.Insert(idp.RequestHeader.ClientCA.Name)
			}
		}
	}
	return refs
}

func proxyConfigMapRefs(spec *configv1.ProxySpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	if len(spec.TrustedCA.Name) > 0 {
		refs.Insert(spec.TrustedCA.Name)
	}
	return refs
}

func schedulerConfigMapRefs(spec *configv1.SchedulerSpec) sets.String {
	refs := sets.NewString()
	if spec == nil {
		return refs
	}
	if len(spec.Policy.Name) > 0 {
		refs.Insert(spec.Policy.Name)
	}
	return refs
}
