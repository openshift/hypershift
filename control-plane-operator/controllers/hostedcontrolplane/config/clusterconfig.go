package config

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/api"
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
}

func ParseGlobalConfig(ctx context.Context, cfg *hyperv1.ClusterConfiguration) (GlobalConfig, error) {
	globalConfig := GlobalConfig{}
	if cfg == nil {
		return globalConfig, nil
	}
	kinds := sets.NewString() // keeps track of which kinds have been found
	for i, cfg := range cfg.Items {
		cfgObject, gvk, err := api.YamlSerializer.Decode(cfg.Raw, nil, nil)
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
			globalConfig.APIServer = obj
		case *configv1.Authentication:
			globalConfig.Authentication = obj
		case *configv1.FeatureGate:
			globalConfig.FeatureGate = obj
		case *configv1.Image:
			globalConfig.Image = obj
		case *configv1.Ingress:
			globalConfig.Ingress = obj
		case *configv1.Network:
			globalConfig.Network = obj
		case *configv1.OAuth:
			globalConfig.OAuth = obj
		case *configv1.Scheduler:
			globalConfig.Scheduler = obj
		default:
			log := ctrl.LoggerFrom(ctx)
			log.Info("WARNING: unrecognized config found", "kind", gvk.Kind)
		}
	}
	return globalConfig, nil
}

func ValidateGlobalConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	if hcp.Spec.Configuration == nil {
		return nil
	}
	gCfg, err := ParseGlobalConfig(ctx, hcp.Spec.Configuration)
	if err != nil {
		return err
	}
	var errs []error
	referencedSecrets := sets.NewString()
	referencedConfigMaps := sets.NewString()

	for _, cmRef := range hcp.Spec.Configuration.ConfigMapRefs {
		referencedConfigMaps.Insert(cmRef.Name)
	}

	for _, secretRef := range hcp.Spec.Configuration.SecretRefs {
		referencedSecrets.Insert(secretRef.Name)
	}

	if gCfg.APIServer != nil {
		refErrs := validateAPIServerReferencedResources(gCfg.APIServer, referencedSecrets, referencedConfigMaps)
		errs = append(errs, refErrs...)
	}
	if gCfg.Authentication != nil {
		refErrs := validateAuthenticationReferencedResources(gCfg.Authentication, referencedSecrets, referencedConfigMaps)
		errs = append(errs, refErrs...)
	}
	// Skipping FeatureGate because it has no referenced resources
	if gCfg.Image != nil {
		refErrs := validateImageReferencedResources(gCfg.Image, referencedSecrets, referencedConfigMaps)
		errs = append(errs, refErrs...)
	}
	// Skipping Ingress because it has no referenced resources
	// Skipping Network because it has no referenced resources
	if gCfg.OAuth != nil {
		refErrs := validateOauthReferencedResources(gCfg.OAuth, referencedSecrets, referencedConfigMaps)
		errs = append(errs, refErrs...)
	}
	if gCfg.Scheduler != nil {
		refErrs := validateSchedulerReferencedResources(gCfg.Scheduler, referencedSecrets, referencedConfigMaps)
		errs = append(errs, refErrs...)
	}
	return utilerrors.NewAggregate(errs)
}

func validateAPIServerReferencedResources(cfg *configv1.APIServer, secrets, configMaps sets.String) []error {
	var errs []error
	for _, namedCert := range cfg.Spec.ServingCerts.NamedCertificates {
		if len(namedCert.ServingCertificate.Name) > 0 {
			if !secrets.Has(namedCert.ServingCertificate.Name) {
				errs = append(errs, fmt.Errorf("APIServer: named serving certificate %s not included in secret references", namedCert.ServingCertificate.Name))
			}
		}
	}
	if len(cfg.Spec.ClientCA.Name) > 0 {
		if !configMaps.Has(cfg.Spec.ClientCA.Name) {
			errs = append(errs, fmt.Errorf("APIServer: client CA configmap %s not included in configmap references", cfg.Spec.ClientCA.Name))
		}
	}
	return errs
}

func validateAuthenticationReferencedResources(cfg *configv1.Authentication, secrets, configMaps sets.String) []error {
	var errs []error
	if len(cfg.Spec.OAuthMetadata.Name) > 0 {
		if !configMaps.Has(cfg.Spec.OAuthMetadata.Name) {
			errs = append(errs, fmt.Errorf("Authentication: oauth metadata configmap %s is not included in configmap references", cfg.Spec.OAuthMetadata.Name))
		}
	}
	if cfg.Spec.WebhookTokenAuthenticator != nil {
		if len(cfg.Spec.WebhookTokenAuthenticator.KubeConfig.Name) > 0 {
			if !secrets.Has(cfg.Spec.WebhookTokenAuthenticator.KubeConfig.Name) {
				errs = append(errs, fmt.Errorf("Authentication: webhook token authenticator kubeconfig %s is not included in secret references", cfg.Spec.WebhookTokenAuthenticator.KubeConfig.Name))
			}
		}
	}
	return errs
}

func validateImageReferencedResources(cfg *configv1.Image, secrets, configMaps sets.String) []error {
	var errs []error
	if len(cfg.Spec.AdditionalTrustedCA.Name) > 0 {
		if !configMaps.Has(cfg.Spec.AdditionalTrustedCA.Name) {
			errs = append(errs, fmt.Errorf("Image: additional trusted CA configmap %s is not included in configmap references", cfg.Spec.AdditionalTrustedCA.Name))
		}
	}
	return errs
}

func validateOauthReferencedResources(cfg *configv1.OAuth, secrets, configMaps sets.String) []error {
	var errs []error
	for _, idp := range cfg.Spec.IdentityProviders {
		switch {
		case idp.BasicAuth != nil:
			if len(idp.BasicAuth.CA.Name) > 0 {
				if !configMaps.Has(idp.BasicAuth.CA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s basicauth CA configmap %s is not included in configmap references", idp.Name, idp.BasicAuth.CA.Name))
				}
			}
			if len(idp.BasicAuth.TLSClientCert.Name) > 0 {
				if !secrets.Has(idp.BasicAuth.TLSClientCert.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s basicauth TLS client cert secret %s is not included in secret references", idp.Name, idp.BasicAuth.TLSClientCert.Name))
				}
			}
			if len(idp.BasicAuth.TLSClientKey.Name) > 0 {
				if !secrets.Has(idp.BasicAuth.TLSClientKey.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s basicauth TLS client key secret %s is not included in secret references", idp.Name, idp.BasicAuth.TLSClientKey.Name))
				}
			}
		case idp.GitHub != nil:
			if len(idp.GitHub.ClientSecret.Name) > 0 {
				if !secrets.Has(idp.GitHub.ClientSecret.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s github client secret %s is not included in referenced secrets", idp.Name, idp.GitHub.ClientSecret.Name))
				}
			}
			if len(idp.GitHub.CA.Name) > 0 {
				if !configMaps.Has(idp.GitHub.CA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s github CA configmap %s is not included in referenced configmaps", idp.Name, idp.GitHub.CA.Name))
				}
			}
		case idp.GitLab != nil:
			if len(idp.GitLab.ClientSecret.Name) > 0 {
				if !secrets.Has(idp.GitLab.ClientSecret.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s gitlab client secret %s is not included in referenced secrets", idp.Name, idp.GitLab.ClientSecret.Name))
				}
			}
			if len(idp.GitLab.CA.Name) > 0 {
				if !configMaps.Has(idp.GitLab.CA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s gitlab CA configmap %s is not included in referenced configmaps", idp.Name, idp.GitLab.CA.Name))
				}
			}
		case idp.Google != nil:
			if len(idp.Google.ClientSecret.Name) > 0 {
				if !secrets.Has(idp.Google.ClientSecret.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s google client secret %s is not included in referenced secrets", idp.Name, idp.Google.ClientSecret.Name))
				}
			}
		case idp.HTPasswd != nil:
			if len(idp.HTPasswd.FileData.Name) > 0 {
				if !secrets.Has(idp.HTPasswd.FileData.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s htpasswd filedata secret %s is not included in referenced secrets", idp.Name, idp.HTPasswd.FileData.Name))
				}
			}
		case idp.Keystone != nil:
			if len(idp.Keystone.CA.Name) > 0 {
				if !configMaps.Has(idp.Keystone.CA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s keystone CA configmap %s is not included in configmap references", idp.Name, idp.Keystone.CA.Name))
				}
			}
			if len(idp.Keystone.TLSClientCert.Name) > 0 {
				if !secrets.Has(idp.Keystone.TLSClientCert.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s keystone TLS client cert secret %s is not included in secret references", idp.Name, idp.Keystone.TLSClientCert.Name))
				}
			}
			if len(idp.Keystone.TLSClientKey.Name) > 0 {
				if !secrets.Has(idp.Keystone.TLSClientKey.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s keystone TLS client key secret %s is not included in secret references", idp.Name, idp.Keystone.TLSClientKey.Name))
				}
			}
		case idp.LDAP != nil:
			if len(idp.LDAP.BindPassword.Name) > 0 {
				if !secrets.Has(idp.LDAP.BindPassword.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s ldap bind password secret %s is not included in referenced secrets", idp.Name, idp.LDAP.BindPassword.Name))
				}
			}
			if len(idp.LDAP.CA.Name) > 0 {
				if !configMaps.Has(idp.LDAP.CA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s ldap CA configmap %s is not included in referenced configmaps", idp.Name, idp.LDAP.CA.Name))
				}
			}
		case idp.OpenID != nil:
			if len(idp.OpenID.ClientSecret.Name) > 0 {
				if !secrets.Has(idp.OpenID.ClientSecret.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s openid client secret %s is not included in referenced secrets", idp.Name, idp.OpenID.ClientSecret.Name))
				}
			}
			if len(idp.OpenID.CA.Name) > 0 {
				if !configMaps.Has(idp.OpenID.CA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s openid CA configmap %s is not included in referenced configmaps", idp.Name, idp.OpenID.CA.Name))
				}
			}
		case idp.RequestHeader != nil:
			if len(idp.RequestHeader.ClientCA.Name) > 0 {
				if !configMaps.Has(idp.RequestHeader.ClientCA.Name) {
					errs = append(errs, fmt.Errorf("OAuth: identity provider %s requestheader client CA configmap %s is not included in referenced configmaps", idp.Name, idp.RequestHeader.ClientCA.Name))
				}
			}
		}
	}
	if len(cfg.Spec.Templates.Error.Name) > 0 {
		if !secrets.Has(cfg.Spec.Templates.Error.Name) {
			errs = append(errs, fmt.Errorf("OAuth: error template secret %s is not included in referenced secrets", cfg.Spec.Templates.Error.Name))
		}
	}
	if len(cfg.Spec.Templates.Login.Name) > 0 {
		if !secrets.Has(cfg.Spec.Templates.Login.Name) {
			errs = append(errs, fmt.Errorf("OAuth: login template secret %s is not included in referenced secrets", cfg.Spec.Templates.Login.Name))
		}
	}
	if len(cfg.Spec.Templates.ProviderSelection.Name) > 0 {
		if !secrets.Has(cfg.Spec.Templates.ProviderSelection.Name) {
			errs = append(errs, fmt.Errorf("OAuth: provider selection template secret %s is not included in referenced secrets", cfg.Spec.Templates.ProviderSelection.Name))
		}
	}
	return errs
}

func validateSchedulerReferencedResources(cfg *configv1.Scheduler, secrets, configMaps sets.String) []error {
	var errs []error
	if len(cfg.Spec.Policy.Name) > 0 {
		if !configMaps.Has(cfg.Spec.Policy.Name) {
			errs = append(errs, fmt.Errorf("Scheduler: policy configmap %s is not included in referenced configmaps", cfg.Spec.Policy.Name))
		}
	}
	return errs
}
