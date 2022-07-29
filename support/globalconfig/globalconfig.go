package globalconfig

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/api"
)

type GlobalConfig struct {
	APIServer      *configv1.APIServer
	Authentication *configv1.Authentication
	FeatureGate    *configv1.FeatureGate
	Image          *configv1.Image
	Ingress        *configv1.Ingress
	Network        *configv1.Network
	OAuth          *configv1.OAuth
	Scheduler      *configv1.Scheduler
	Proxy          *configv1.Proxy
	Build          *configv1.Build
	Project        *configv1.Project
}

type ObservedConfig struct {
	Image   *configv1.Image
	Build   *configv1.Build
	Project *configv1.Project
}

func ParseGlobalConfig(ctx context.Context, cfg *hyperv1.ClusterConfiguration) (GlobalConfig, error) {
	globalConfig := GlobalConfig{}
	if cfg == nil {
		return globalConfig, nil
	}
	kinds := sets.NewString() // keeps track of which kinds have been found
	for i, cfg := range cfg.Items {
		cfgObject, gvk, err := api.TolerantYAMLSerializer.Decode(cfg.Raw, nil, nil)
		if err != nil {
			return globalConfig, fmt.Errorf("cannot parse configuration at index %d: %w", i, err)
		}
		if gvk.GroupVersion().String() != configv1.GroupVersion.String() {
			return globalConfig, fmt.Errorf("invalid resource type found in configuration: kind: %s, apiVersion: %s", gvk.Kind, gvk.GroupVersion().String())
		}
		if kinds.Has(gvk.Kind) {
			return globalConfig, fmt.Errorf("duplicate config type found: %s", gvk.Kind)
		}
		kinds.Insert(gvk.Kind)
		switch obj := cfgObject.(type) {
		case *configv1.APIServer:
			if obj.Spec.Audit.Profile == "" {
				// Populate kubebuilder default for comparison
				// https://github.com/openshift/api/blob/f120778bee805ad1a7a4f05a6430332cf5811813/config/v1/types_apiserver.go#L57
				obj.Spec.Audit.Profile = configv1.DefaultAuditProfileType
			}
			globalConfig.APIServer = obj
		case *configv1.Authentication:
			globalConfig.Authentication = obj
		case *configv1.FeatureGate:
			globalConfig.FeatureGate = obj
		case *configv1.Ingress:
			globalConfig.Ingress = obj
		case *configv1.Network:
			globalConfig.Network = obj
		case *configv1.OAuth:
			globalConfig.OAuth = obj
		case *configv1.Scheduler:
			globalConfig.Scheduler = obj
		case *configv1.Proxy:
			globalConfig.Proxy = obj
		default:
			log := ctrl.LoggerFrom(ctx)
			log.Info("WARNING: unrecognized config found", "kind", gvk.Kind)
		}
	}
	return globalConfig, nil
}

func SecretRefs(cfg *hyperv1.ClusterConfiguration) []string {
	result := sets.NewString()
	result = result.Union(apiServerSecretRefs(cfg.APIServer))
	result = result.Union(authenticationSecretRefs(cfg.Authentication))
	result = result.Union(ingressSecretRefs(cfg.Ingress))
	result = result.Union(oauthSecretRefs(cfg.OAuth))
	return result.List()
}

func ConfigMapRefs(cfg *hyperv1.ClusterConfiguration) []string {
	result := sets.NewString()
	result = result.Union(apiServerConfigMapRefs(cfg.APIServer))
	result = result.Union(authenticationConfigMapRefs(cfg.Authentication))
	result = result.Union(imageConfigMapRefs(cfg.Image))
	result = result.Union(oauthConfigMapRefs(cfg.OAuth))
	result = result.Union(proxyConfigMapRefs(cfg.Proxy))
	result = result.Union(schedulerConfigMapRefs(cfg.Scheduler))
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
