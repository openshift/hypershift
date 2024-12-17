package oauth

import (
	"bytes"
	"context"
	"fmt"
	"path"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	OAuthServerConfigKey = "config.yaml"

	defaultAuthorizeTokenMaxAgeSeconds = int32(300)
)

func serializeOsinConfig(cfg *osinv1.OsinServerConfig) ([]byte, error) {
	out := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(cfg, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ReconcileOAuthServerConfig(ctx context.Context, cm *corev1.ConfigMap, ownerRef config.OwnerRef, client crclient.Client, params *OAuthConfigParams) error {
	ownerRef.ApplyTo(cm)
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	generatedConfig, err := generateOAuthConfig(ctx, client, cm.Namespace, params)
	if err != nil {
		return fmt.Errorf("failed to generate oauth config: %w", err)
	}
	b, err := serializeOsinConfig(generatedConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize oauth server config: %w", err)
	}
	cm.Data[OAuthServerConfigKey] = string(b)
	return nil
}

func generateOAuthConfig(ctx context.Context, client crclient.Client, namespace string, params *OAuthConfigParams) (*osinv1.OsinServerConfig, error) {
	// Ignore the error here since we don't want to fail the deployment if the identity providers are invalid
	// A condition will be set on the HC to indicate the error
	identityProviders, _, _ := ConvertIdentityProviders(ctx, params.IdentityProviders, params.OauthConfigOverrides, client, namespace)

	cpath := func(volume, file string) string {
		dir := volumeMounts.Path(oauthContainerMain().Name, volume)
		return path.Join(dir, file)
	}

	caCertPath := cpath(oauthVolumeMasterCABundle().Name, certs.CASignerCertMapKey)
	serverConfig := &osinv1.OsinServerConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OsinServerConfig",
			APIVersion: osinv1.GroupVersion.String(),
		},
		GenericAPIServerConfig: configv1.GenericAPIServerConfig{
			ServingInfo: configv1.HTTPServingInfo{
				ServingInfo: configv1.ServingInfo{
					BindAddress:       fmt.Sprintf("0.0.0.0:%d", OAuthServerPort),
					BindNetwork:       "tcp",
					NamedCertificates: globalconfig.GetConfigNamedCertificates(params.NamedCertificates, oauthNamedCertificateMountPathPrefix),
					CertInfo: configv1.CertInfo{
						CertFile: cpath(oauthVolumeServingCert().Name, corev1.TLSCertKey),
						KeyFile:  cpath(oauthVolumeServingCert().Name, corev1.TLSPrivateKeyKey),
					},
					CipherSuites:  params.CipherSuites,
					MinTLSVersion: params.MinTLSVersion,
					ClientCA:      "",
				},
				MaxRequestsInFlight:   1000,
				RequestTimeoutSeconds: 5 * 60,
			},
			AuditConfig: configv1.AuditConfig{},
			KubeClientConfig: configv1.KubeClientConfig{
				KubeConfig: cpath(oauthVolumeKubeconfig().Name, kas.KubeconfigKey),
				ConnectionOverrides: configv1.ClientConnectionOverrides{
					QPS:   400,
					Burst: 400,
				},
			},
		},
		OAuthConfig: osinv1.OAuthConfig{
			MasterCA:                    &caCertPath,
			MasterURL:                   fmt.Sprintf("https://%s:%d", params.ExternalHost, params.ExternalPort),
			MasterPublicURL:             fmt.Sprintf("https://%s:%d", params.ExternalHost, params.ExternalPort),
			LoginURL:                    fmt.Sprintf("https://%s:%d", params.ExternalAPIHost, params.ExternalAPIPort),
			AlwaysShowProviderSelection: false,
			GrantConfig: osinv1.GrantConfig{
				Method:               osinv1.GrantHandlerDeny, // force denial as this field must be set per OAuth client
				ServiceAccountMethod: osinv1.GrantHandlerPrompt,
			},
			SessionConfig: &osinv1.SessionConfig{
				SessionSecretsFile:   cpath(oauthVolumeSessionSecret().Name, SessionSecretsFileKey),
				SessionMaxAgeSeconds: 5 * 60, // 5 minutes
				SessionName:          "ssn",
			},
			Templates: &osinv1.OAuthTemplates{
				Login:             cpath(oauthVolumeLoginTemplate().Name, LoginTemplateKey),
				ProviderSelection: cpath(oauthVolumeProvidersTemplate().Name, ProviderSelectionTemplateKey),
				Error:             cpath(oauthVolumeErrorTemplate().Name, ErrorsTemplateKey),
			},
			TokenConfig: osinv1.TokenConfig{
				AccessTokenMaxAgeSeconds:     params.AccessTokenMaxAgeSeconds,
				AccessTokenInactivityTimeout: params.AccessTokenInactivityTimeout,
				AuthorizeTokenMaxAgeSeconds:  defaultAuthorizeTokenMaxAgeSeconds,
			},
			IdentityProviders: identityProviders,
		},
	}
	if len(params.LoginURLOverride) > 0 {
		serverConfig.OAuthConfig.LoginURL = params.LoginURLOverride
	}
	return serverConfig, nil
}
