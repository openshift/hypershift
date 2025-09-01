package configrefs

import (
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/sets"
)

// Be sure to update *ConfigMapRefs in refs.go to include new configmap refs
func TestKnownConfigMapRefs(t *testing.T) {
	actual := findRefs(reflect.TypeOf(hyperv1.ClusterConfiguration{}), "", "ConfigMapNameReference")
	expected := sets.New[string](
		".APIServer.ClientCA",
		".Authentication.OAuthMetadata",
		".Authentication.OIDCProviders.Issuer.CertificateAuthority",
		".Image.AdditionalTrustedCA",
		".OAuth.IdentityProviders.IdentityProviderConfig.BasicAuth.OAuthRemoteConnectionInfo.CA",
		".OAuth.IdentityProviders.IdentityProviderConfig.GitHub.CA",
		".OAuth.IdentityProviders.IdentityProviderConfig.GitLab.CA",
		".OAuth.IdentityProviders.IdentityProviderConfig.Keystone.OAuthRemoteConnectionInfo.CA",
		".OAuth.IdentityProviders.IdentityProviderConfig.LDAP.CA",
		".OAuth.IdentityProviders.IdentityProviderConfig.OpenID.CA",
		".OAuth.IdentityProviders.IdentityProviderConfig.RequestHeader.ClientCA",
		".Proxy.TrustedCA",
		".Scheduler.Policy",
	)
	if !actual.Equal(expected) {
		t.Errorf("actual: %v, expected: %v", actual.UnsortedList(), expected.UnsortedList())
	}
}

func TestConfigMapRefs(t *testing.T) {
	tests := []struct {
		name   string
		config *hyperv1.ClusterConfiguration
		refs   []string
	}{
		{
			name:   "none",
			config: &hyperv1.ClusterConfiguration{},
			refs:   []string{},
		},
		{
			name: "apiserver ca",
			config: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ServingCerts: configv1.APIServerServingCerts{
						NamedCertificates: []configv1.APIServerNamedServingCert{
							{
								ServingCertificate: configv1.SecretNameReference{
									Name: "servingcertref",
								},
							},
						},
					},
					ClientCA: configv1.ConfigMapNameReference{
						Name: "caref",
					},
				},
			},
			refs: []string{"caref"},
		},
		{
			name: "oauth metadata",
			config: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ServingCerts: configv1.APIServerServingCerts{
						NamedCertificates: []configv1.APIServerNamedServingCert{
							{
								ServingCertificate: configv1.SecretNameReference{
									Name: "servingcertref",
								},
							},
						},
					},
				},
				Authentication: &configv1.AuthenticationSpec{
					OAuthMetadata: configv1.ConfigMapNameReference{
						Name: "oauthmetadataref",
					},
				},
			},
			refs: []string{"oauthmetadataref"},
		},
		{
			name: "oidc provider",
			config: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
					OIDCProviders: []configv1.OIDCProvider{
						{
							Issuer: configv1.TokenIssuer{
								CertificateAuthority: configv1.ConfigMapNameReference{
									Name: "issuercaref",
								},
							},
						},
					},
				},
			},
			refs: []string{"issuercaref"},
		},
		{
			name: "image ca",
			config: &hyperv1.ClusterConfiguration{
				Image: &configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{
						Name: "imagecaref",
					},
				},
			},
			refs: []string{"imagecaref"},
		},
		{
			name: "idp refs",
			config: &hyperv1.ClusterConfiguration{
				OAuth: &configv1.OAuthSpec{
					IdentityProviders: []configv1.IdentityProvider{
						{
							Name: "basicauth",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeBasicAuth,
								BasicAuth: &configv1.BasicAuthIdentityProvider{
									OAuthRemoteConnectionInfo: configv1.OAuthRemoteConnectionInfo{
										CA: configv1.ConfigMapNameReference{
											Name: "basicauth-caref",
										},
									},
								},
							},
						},
						{
							Name: "github",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeGitHub,
								GitHub: &configv1.GitHubIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "github-caref",
									},
								},
							},
						},
						{
							Name: "gitlab",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeGitLab,
								GitLab: &configv1.GitLabIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "gitlab-caref",
									},
								},
							},
						},
						{
							// Should not be included because it has no configmap refs
							Name: "google",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeGoogle,
								Google: &configv1.GoogleIdentityProvider{
									ClientSecret: configv1.SecretNameReference{
										Name: "google-secretref",
									},
								},
							},
						},
						{
							// Should not be included because it has no configmap refs
							Name: "htpasswd",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeHTPasswd,
								HTPasswd: &configv1.HTPasswdIdentityProvider{
									FileData: configv1.SecretNameReference{
										Name: "file-secretref",
									},
								},
							},
						},
						{
							Name: "keystone",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeKeystone,
								Keystone: &configv1.KeystoneIdentityProvider{
									OAuthRemoteConnectionInfo: configv1.OAuthRemoteConnectionInfo{
										CA: configv1.ConfigMapNameReference{
											Name: "keystone-caref",
										},
									},
								},
							},
						},
						{
							Name: "ldap",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeLDAP,
								LDAP: &configv1.LDAPIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "ldap-caref",
									},
								},
							},
						},
						{
							Name: "openid",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeOpenID,
								OpenID: &configv1.OpenIDIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "openid-caref",
									},
								},
							},
						},
						{
							Name: "requestheader",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeRequestHeader,
								RequestHeader: &configv1.RequestHeaderIdentityProvider{
									ClientCA: configv1.ConfigMapNameReference{
										Name: "requestheader-caref",
									},
								},
							},
						},
					},
				},
			},
			refs: []string{"basicauth-caref", "github-caref", "gitlab-caref", "keystone-caref", "ldap-caref", "openid-caref", "requestheader-caref"},
		},
		{
			name: "proxy ca",
			config: &hyperv1.ClusterConfiguration{
				Proxy: &configv1.ProxySpec{
					TrustedCA: configv1.ConfigMapNameReference{
						Name: "proxy-caref",
					},
				},
			},
			refs: []string{"proxy-caref"},
		},
		{
			name: "apiserver and scheduler",
			config: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ClientCA: configv1.ConfigMapNameReference{
						Name: "caref",
					},
				},
				Scheduler: &configv1.SchedulerSpec{
					Policy: configv1.ConfigMapNameReference{
						Name: "policyref",
					},
				},
			},
			refs: []string{"caref", "policyref"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualRefs := ConfigMapRefs(test.config)
			if actual, expected := sets.NewString(actualRefs...), sets.NewString(test.refs...); !actual.Equal(expected) {
				t.Errorf("actual: %v, expected: %v", actual.List(), expected.List())
			}
		})
	}
}

// Be sure to update *SecretRefs() in refs.go to include new secret refs
func TestKnownSecretRefs(t *testing.T) {
	actual := findRefs(reflect.TypeOf(hyperv1.ClusterConfiguration{}), "", "SecretNameReference")
	expected := sets.New[string](
		".APIServer.ServingCerts.NamedCertificates.ServingCertificate",
		".Authentication.WebhookTokenAuthenticator.KubeConfig",
		".Authentication.WebhookTokenAuthenticators.KubeConfig",
		".Authentication.OIDCProviders.OIDCClients.ClientSecret",
		".Ingress.ComponentRoutes.ServingCertKeyPairSecret",
		".OAuth.IdentityProviders.IdentityProviderConfig.BasicAuth.OAuthRemoteConnectionInfo.TLSClientCert",
		".OAuth.IdentityProviders.IdentityProviderConfig.BasicAuth.OAuthRemoteConnectionInfo.TLSClientKey",
		".OAuth.IdentityProviders.IdentityProviderConfig.GitHub.ClientSecret",
		".OAuth.IdentityProviders.IdentityProviderConfig.GitLab.ClientSecret",
		".OAuth.IdentityProviders.IdentityProviderConfig.Google.ClientSecret",
		".OAuth.IdentityProviders.IdentityProviderConfig.HTPasswd.FileData",
		".OAuth.IdentityProviders.IdentityProviderConfig.Keystone.OAuthRemoteConnectionInfo.TLSClientCert",
		".OAuth.IdentityProviders.IdentityProviderConfig.Keystone.OAuthRemoteConnectionInfo.TLSClientKey",
		".OAuth.IdentityProviders.IdentityProviderConfig.LDAP.BindPassword",
		".OAuth.IdentityProviders.IdentityProviderConfig.OpenID.ClientSecret",
		".OAuth.Templates.Error",
		".OAuth.Templates.Login",
		".OAuth.Templates.ProviderSelection",
	)
	if !actual.Equal(expected) {
		t.Errorf("actual: %v, expected: %v", actual.UnsortedList(), expected.UnsortedList())
	}
}

func TestSecretRefs(t *testing.T) {
	tests := []struct {
		name   string
		config *hyperv1.ClusterConfiguration
		refs   []string
	}{
		{
			name:   "none",
			config: &hyperv1.ClusterConfiguration{},
			refs:   []string{},
		},
		{
			name: "apiserver certrefs",
			config: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ServingCerts: configv1.APIServerServingCerts{
						NamedCertificates: []configv1.APIServerNamedServingCert{
							{
								Names: []string{"foo"},
								ServingCertificate: configv1.SecretNameReference{
									Name: "servingcertref-1",
								},
							},
							{
								Names: []string{"bar"},
								ServingCertificate: configv1.SecretNameReference{
									Name: "servingcertref-2",
								},
							},
						},
					},
				},
			},
			refs: []string{"servingcertref-1", "servingcertref-2"},
		},
		{
			name: "auth kubeconfig",
			config: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					WebhookTokenAuthenticator: &configv1.WebhookTokenAuthenticator{
						KubeConfig: configv1.SecretNameReference{
							Name: "kubeconfig-ref",
						},
					},
				},
			},
			refs: []string{"kubeconfig-ref"},
		},
		{
			name: "auth kubeconfig - deprecated",
			config: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					WebhookTokenAuthenticators: []configv1.DeprecatedWebhookTokenAuthenticator{
						{
							KubeConfig: configv1.SecretNameReference{
								Name: "kubeconfig-ref1",
							},
						},
						{
							KubeConfig: configv1.SecretNameReference{
								Name: "kubeconfig-ref2",
							},
						},
					},
				},
			},
			refs: []string{"kubeconfig-ref1", "kubeconfig-ref2"},
		},
		{
			name: "ingress component serving cert",
			config: &hyperv1.ClusterConfiguration{
				Ingress: &configv1.IngressSpec{
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "serving-cert1",
							},
						},
						{
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "serving-cert2",
							},
						},
					},
				},
			},
			refs: []string{"serving-cert1", "serving-cert2"},
		},
		{
			name: "oidc client secret",
			config: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
					OIDCProviders: []configv1.OIDCProvider{
						{
							OIDCClients: []configv1.OIDCClientConfig{
								{
									ClientSecret: configv1.SecretNameReference{
										Name: "clientsecretref",
									},
								},
							},
						},
					},
				},
			},
			refs: []string{"clientsecretref"},
		},
		{
			name: "idp refs",
			config: &hyperv1.ClusterConfiguration{
				OAuth: &configv1.OAuthSpec{
					IdentityProviders: []configv1.IdentityProvider{
						{
							Name: "basicauth",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeBasicAuth,
								BasicAuth: &configv1.BasicAuthIdentityProvider{
									OAuthRemoteConnectionInfo: configv1.OAuthRemoteConnectionInfo{
										CA: configv1.ConfigMapNameReference{
											Name: "basicauth-caref",
										},
										TLSClientCert: configv1.SecretNameReference{
											Name: "basicauth-cert-ref",
										},
										TLSClientKey: configv1.SecretNameReference{
											Name: "basicauth-key-ref",
										},
									},
								},
							},
						},
						{
							Name: "github",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeGitHub,
								GitHub: &configv1.GitHubIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "github-caref",
									},
									ClientSecret: configv1.SecretNameReference{
										Name: "github-clientref",
									},
								},
							},
						},
						{
							Name: "gitlab",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeGitLab,
								GitLab: &configv1.GitLabIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "gitlab-caref",
									},
									ClientSecret: configv1.SecretNameReference{
										Name: "gitlab-clientref",
									},
								},
							},
						},
						{
							Name: "google",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeGoogle,
								Google: &configv1.GoogleIdentityProvider{
									ClientSecret: configv1.SecretNameReference{
										Name: "google-secretref",
									},
								},
							},
						},
						{
							Name: "htpasswd",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeHTPasswd,
								HTPasswd: &configv1.HTPasswdIdentityProvider{
									FileData: configv1.SecretNameReference{
										Name: "file-secretref",
									},
								},
							},
						},
						{
							Name: "keystone",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeKeystone,
								Keystone: &configv1.KeystoneIdentityProvider{
									OAuthRemoteConnectionInfo: configv1.OAuthRemoteConnectionInfo{
										CA: configv1.ConfigMapNameReference{
											Name: "keystone-caref",
										},
										TLSClientCert: configv1.SecretNameReference{
											Name: "keystone-certref",
										},
										TLSClientKey: configv1.SecretNameReference{
											Name: "keystone-keyref",
										},
									},
								},
							},
						},
						{
							Name: "ldap",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeLDAP,
								LDAP: &configv1.LDAPIdentityProvider{
									BindPassword: configv1.SecretNameReference{
										Name: "ldap-passwordref",
									},
									CA: configv1.ConfigMapNameReference{
										Name: "ldap-caref",
									},
								},
							},
						},
						{
							Name: "openid",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeOpenID,
								OpenID: &configv1.OpenIDIdentityProvider{
									CA: configv1.ConfigMapNameReference{
										Name: "openid-caref",
									},
									ClientSecret: configv1.SecretNameReference{
										Name: "openid-secretref",
									},
								},
							},
						},
						{
							Name: "requestheader",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeRequestHeader,
								RequestHeader: &configv1.RequestHeaderIdentityProvider{
									ClientCA: configv1.ConfigMapNameReference{
										Name: "requestheader-caref",
									},
								},
							},
						},
					},
				},
			},
			refs: []string{
				"basicauth-cert-ref",
				"basicauth-key-ref",
				"github-clientref",
				"gitlab-clientref",
				"file-secretref",
				"google-secretref",
				"keystone-certref",
				"keystone-keyref",
				"ldap-passwordref",
				"openid-secretref",
			},
		},
		{
			name: "oauth templates",
			config: &hyperv1.ClusterConfiguration{
				OAuth: &configv1.OAuthSpec{
					Templates: configv1.OAuthTemplates{
						Login: configv1.SecretNameReference{
							Name: "login-ref",
						},
						ProviderSelection: configv1.SecretNameReference{
							Name: "providersel-ref",
						},
						Error: configv1.SecretNameReference{
							Name: "error-ref",
						},
					},
				},
			},
			refs: []string{"login-ref", "providersel-ref", "error-ref"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualRefs := SecretRefs(test.config)
			if actual, expected := sets.New[string](actualRefs...), sets.New[string](test.refs...); !actual.Equal(expected) {
				t.Errorf("actual: %v, expected: %v", actual.UnsortedList(), expected.UnsortedList())
			}
		})
	}
}

func findRefs(sel reflect.Type, prefix, structName string) sets.Set[string] {
	switch sel.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return findRefs(sel.Elem(), prefix, structName)
	case reflect.Struct:
		result := sets.New[string]()
		for i := 0; i < sel.NumField(); i++ {
			field := sel.Field(i)
			if field.Type.Name() == structName {
				result.Insert(prefix + "." + field.Name)
				continue
			}
			fieldRefs := findRefs(field.Type, prefix+"."+field.Name, structName)
			result.Insert(fieldRefs.UnsortedList()...)
		}
		return result
	}
	return sets.New[string]()
}
