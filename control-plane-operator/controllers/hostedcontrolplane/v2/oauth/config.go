package oauth

import (
	"encoding/json"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"

	osinv1 "github.com/openshift/api/osin/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	oauthServerConfigKey     = "config.yaml"
	auditPolicyProfileMapKey = "profile"

	defaultAccessTokenMaxAgeSeconds int32 = 86400
)

// ConfigOverride defines the oauth parameters that can be overridden in special use cases. The only supported
// use case for this currently is the IBMCloud IAM OIDC provider. These parameters are necessary since the public
// OpenID api does not support some customizations used in the IBMCloud IAM OIDC provider. This can be removed
// if the public API is adjusted to allow specifying these customizations.
type ConfigOverride struct {
	URLs      osinv1.OpenIDURLs   `json:"urls,omitempty"`
	Claims    osinv1.OpenIDClaims `json:"claims,omitempty"`
	Challenge *bool               `json:"challenge,omitempty"`
}

func adaptAuditConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	auditConfig := cpContext.HCP.Spec.Configuration.GetAuditPolicyConfig()
	cm.Data[auditPolicyProfileMapKey] = string(auditConfig.Profile)
	return nil
}

func adaptConfigMap(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	if configStr, exists := cm.Data[oauthServerConfigKey]; !exists || len(configStr) == 0 {
		return fmt.Errorf("expected an existing oauth server configuration")
	}

	oauthConfig := &osinv1.OsinServerConfig{}
	if err := util.DeserializeResource(cm.Data[oauthServerConfigKey], oauthConfig, api.Scheme); err != nil {
		return fmt.Errorf("failed to decode existing oauth server configuration: %w", err)
	}

	adaptOAuthConfig(cpContext, oauthConfig)
	serializedConfig, err := util.SerializeResource(oauthConfig, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize oauth server configuration: %w", err)
	}
	cm.Data[oauthServerConfigKey] = serializedConfig
	return nil
}

func adaptOAuthConfig(cpContext component.WorkloadContext, cfg *osinv1.OsinServerConfig) {
	configuration := cpContext.HCP.Spec.Configuration

	cfg.ServingInfo.NamedCertificates = globalconfig.GetConfigNamedCertificates(configuration.GetNamedCertificates(), oauthNamedCertificateMountPathPrefix)

	cfg.ServingInfo.MinTLSVersion = config.MinTLSVersion(configuration.GetTLSSecurityProfile())
	cfg.ServingInfo.CipherSuites = config.CipherSuites(configuration.GetTLSSecurityProfile())

	masterUrl := fmt.Sprintf("https://%s:%d", cpContext.InfraStatus.OAuthHost, cpContext.InfraStatus.OAuthPort)
	controlPlaneEndpoint := cpContext.HCP.Status.ControlPlaneEndpoint
	cfg.OAuthConfig.MasterURL = masterUrl
	cfg.OAuthConfig.MasterPublicURL = masterUrl
	cfg.OAuthConfig.LoginURL = fmt.Sprintf("https://%s:%d", controlPlaneEndpoint.Host, controlPlaneEndpoint.Port)
	// loginURLOverride can be used to specify an override for the oauth config login url. The need for this arises
	// when the login a provider uses doesn't conform to the standard login url in hypershift. The only supported use case
	// for this is IBMCloud Red Hat Openshift
	if loginURLOverride, exist := cpContext.HCP.Annotations[hyperv1.OauthLoginURLOverrideAnnotation]; exist {
		cfg.OAuthConfig.LoginURL = loginURLOverride
	}

	cfg.OAuthConfig.TokenConfig.AccessTokenMaxAgeSeconds = accessTokenMaxAgeSeconds(configuration)
	cfg.OAuthConfig.TokenConfig.AccessTokenInactivityTimeout = accessTokenInactivityTimeout(configuration)

	// var identityProviders []osinv1.IdentityProvider
	if configuration != nil && configuration.OAuth != nil {
		// Ignore the error here since we don't want to fail the deployment if the identity providers are invalid
		// A condition will be set on the HC to indicate the error
		identityProviders, _, _ := ConvertIdentityProviders(cpContext, configuration.OAuth.IdentityProviders, providerOverrides(cpContext.HCP), cpContext.Client, cpContext.HCP.Namespace)
		cfg.OAuthConfig.IdentityProviders = identityProviders
	}
}

func providerOverrides(hcp *hyperv1.HostedControlPlane) map[string]*ConfigOverride {
	overrides := map[string]*ConfigOverride{}
	for annotationKey, annotationValue := range hcp.Annotations {
		if !strings.HasPrefix(annotationKey, hyperv1.IdentityProviderOverridesAnnotationPrefix) {
			continue
		}

		tokenizedString := strings.Split(annotationKey, hyperv1.IdentityProviderOverridesAnnotationPrefix)
		if len(tokenizedString) == 2 {
			identityProvider := tokenizedString[1]
			providerConfigOverride := &ConfigOverride{}
			err := json.Unmarshal([]byte(annotationValue), providerConfigOverride)
			if err == nil {
				overrides[identityProvider] = providerConfigOverride
			}
		}
	}

	return overrides
}

func accessTokenMaxAgeSeconds(configuration *hyperv1.ClusterConfiguration) int32 {
	if configuration != nil && configuration.OAuth != nil && configuration.OAuth.TokenConfig.AccessTokenMaxAgeSeconds > 0 {
		return configuration.OAuth.TokenConfig.AccessTokenMaxAgeSeconds
	}
	return defaultAccessTokenMaxAgeSeconds
}

func accessTokenInactivityTimeout(configuration *hyperv1.ClusterConfiguration) *metav1.Duration {
	if configuration != nil && configuration.OAuth != nil {
		return configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout
	}
	return nil
}
