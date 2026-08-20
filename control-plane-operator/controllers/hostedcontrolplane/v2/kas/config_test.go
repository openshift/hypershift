package kas

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	podsecurityadmissionv1 "k8s.io/pod-security-admission/admission/api/v1"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func TestGenerateConfig(t *testing.T) {
	type testcase struct {
		name     string
		params   KubeAPIServerConfigParams
		expected *kcpv1.KubeAPIServerConfig
	}

	testcases := []testcase{
		{
			name:     "When using default params, it should return default config",
			params:   KubeAPIServerConfigParams{},
			expected: defaultKASConfig(),
		},
		{
			name: "When additional named certificates are provided, it should add them to serving info",
			params: KubeAPIServerConfigParams{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						Names: []string{
							"foo",
							"bar",
						},
						ServingCertificate: configv1.SecretNameReference{
							Name: "foobar-crt",
						},
					},
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ServingInfo.NamedCertificates = append([]configv1.NamedCertificate{
						{
							Names: []string{
								"foo",
								"bar",
							},
							CertInfo: configv1.CertInfo{
								CertFile: "/etc/kubernetes/certs/named-1/tls.crt",
								KeyFile:  "/etc/kubernetes/certs/named-1/tls.key",
							},
						},
					}, kasc.ServingInfo.NamedCertificates...)
				},
			),
		},
		{
			name: "When ExternalIPRanger is configured with AutoAssignCIDRs, it should enable allowIngressIP",
			params: KubeAPIServerConfigParams{
				ExternalIPConfig: &configv1.ExternalIPConfig{
					Policy: &configv1.ExternalIPPolicy{
						AllowedCIDRs: []string{
							"10.0.0.0/16",
						},
						RejectedCIDRs: []string{
							"192.0.2.0/24",
						},
					},
					AutoAssignCIDRs: []string{
						"10.0.0.0/16",
					},
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.AdmissionConfig.PluginConfig["network.openshift.io/ExternalIPRanger"] = configv1.AdmissionPluginConfig{
						Location: "",
						Configuration: runtime.RawExtension{
							Object: &unstructured.Unstructured{
								Object: map[string]interface{}{
									"apiVersion": "network.openshift.io/v1",
									"kind":       "ExternalIPRangerAdmissionConfig",
									"externalIPNetworkCIDRs": []any{
										"!192.0.2.0/24",
										"10.0.0.0/16",
									},
									"allowIngressIP": true,
								},
							},
						},
					}
				},
			),
		},
		{
			name: "When ExternalIPRanger is configured without AutoAssignCIDRs, it should disable allowIngressIP",
			params: KubeAPIServerConfigParams{
				ExternalIPConfig: &configv1.ExternalIPConfig{
					Policy: &configv1.ExternalIPPolicy{
						AllowedCIDRs: []string{
							"10.0.0.0/16",
						},
						RejectedCIDRs: []string{
							"192.0.2.0/24",
						},
					},
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.AdmissionConfig.PluginConfig["network.openshift.io/ExternalIPRanger"] = configv1.AdmissionPluginConfig{
						Location: "",
						Configuration: runtime.RawExtension{
							Object: &unstructured.Unstructured{
								Object: map[string]interface{}{
									"apiVersion": "network.openshift.io/v1",
									"kind":       "ExternalIPRangerAdmissionConfig",
									"externalIPNetworkCIDRs": []any{
										"!192.0.2.0/24",
										"10.0.0.0/16",
									},
									"allowIngressIP": false,
								},
							},
						},
					}
				},
			),
		},
		{
			name: "When ClusterNetwork and ServiceNetwork are configured, it should set restricted CIDRs and services subnet",
			params: KubeAPIServerConfigParams{
				ClusterNetwork: []string{
					"10.0.0.0/16",
				},
				ServiceNetwork: []string{
					"192.0.2.0/24",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.AdmissionConfig.PluginConfig["network.openshift.io/RestrictedEndpointsAdmission"] = configv1.AdmissionPluginConfig{
						Location: "",
						Configuration: runtime.RawExtension{
							Object: &unstructured.Unstructured{
								Object: map[string]interface{}{
									"apiVersion": "network.openshift.io/v1",
									"kind":       "RestrictedEndpointsAdmissionConfig",
									"restrictedCIDRs": []any{
										"10.0.0.0/16",
										"192.0.2.0/24",
									},
								},
							},
						},
					}
				},
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ServicesSubnet = "192.0.2.0/24"
				},
			),
		},
		{
			name: "When KAS Pod port is configured, it should set bind address",
			params: KubeAPIServerConfigParams{
				KASPodPort: 8080,
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ServingInfo.BindAddress = "0.0.0.0:8080"
				},
			),
		},
		{
			name: "When TLS profile is configured, it should set TLS version and cipher suites",
			params: KubeAPIServerConfigParams{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileModernType,
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ServingInfo.MinTLSVersion = string(configv1.VersionTLS13)

					// TODO: Is this expected?
					kasc.ServingInfo.CipherSuites = []string{}
				},
			),
		},
		{
			name: "When additional CORS allowed origins are provided, it should append them to the list",
			params: KubeAPIServerConfigParams{
				AdditionalCORSAllowedOrigins: []string{
					"abcdef",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.CORSAllowedOrigins = append(kasc.CORSAllowedOrigins, "abcdef")
				},
			),
		},
		{
			name: "When console public URL is configured, it should set the console public URL",
			params: KubeAPIServerConfigParams{
				ConsolePublicURL: "https://console.public.io",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ConsolePublicURL = "https://console.public.io"
				},
			),
		},
		{
			name: "When image policy is configured, it should set registry hostnames",
			params: KubeAPIServerConfigParams{
				InternalRegistryHostName: "internal",
				ExternalRegistryHostNames: []string{
					"external-one",
					"external-two",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ImagePolicyConfig = kcpv1.KubeAPIServerImagePolicyConfig{
						InternalRegistryHostname: "internal",
						ExternalRegistryHostnames: []string{
							"external-one",
							"external-two",
						},
					}
				},
			),
		},
		{
			name: "When default node selector is configured, it should set project config",
			params: KubeAPIServerConfigParams{
				DefaultNodeSelector: "foo=bar",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.ProjectConfig = kcpv1.KubeAPIServerProjectConfig{
						DefaultNodeSelector: "foo=bar",
					}
				},
			),
		},
		{
			name: "When OpenShiftPodSecurityAdmission feature gate is enabled, it should configure restricted pod security defaults",
			params: KubeAPIServerConfigParams{
				FeatureGates: []string{
					"OpenShiftPodSecurityAdmission=true",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.AdmissionConfig.PluginConfig["PodSecurity"] = configv1.AdmissionPluginConfig{
						Configuration: runtime.RawExtension{
							Object: &podsecurityadmissionv1.PodSecurityConfiguration{
								TypeMeta: metav1.TypeMeta{
									APIVersion: podsecurityadmissionv1.SchemeGroupVersion.String(),
									Kind:       "PodSecurityConfiguration",
								},
								Defaults: podsecurityadmissionv1.PodSecurityDefaults{
									Enforce:        "restricted",
									EnforceVersion: "latest",
									Audit:          "restricted",
									AuditVersion:   "latest",
									Warn:           "restricted",
									WarnVersion:    "latest",
								},
								Exemptions: podsecurityadmissionv1.PodSecurityExemptions{
									Usernames: []string{
										"system:serviceaccount:openshift-infra:build-controller",
									},
								},
							},
						},
					}
				},
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["feature-gates"] = append(kcpv1.Arguments{"OpenShiftPodSecurityAdmission=true"}, kasc.APIServerArguments["feature-gates"]...)
				},
			),
		},
		{
			name: "When auth type is None, it should clear OAuth metadata file",
			params: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeNone,
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.AuthConfig.OAuthMetadataFile = ""
				},
			),
		},
		{
			name: "When advertise address is provided, it should set the advertise-address argument",
			params: KubeAPIServerConfigParams{
				AdvertiseAddress: "foo",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["advertise-address"] = kcpv1.Arguments{"foo"}
				},
			),
		},
		{
			name: "When service account issuer URL is provided, it should configure SA issuer arguments",
			params: KubeAPIServerConfigParams{
				ServiceAccountIssuerURL: "https://issuer.io",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["api-audiences"] = kcpv1.Arguments{"https://issuer.io"}
					kasc.APIServerArguments["service-account-issuer"] = kcpv1.Arguments{"https://issuer.io"}
					kasc.APIServerArguments["service-account-jwks-uri"] = kcpv1.Arguments{"https://issuer.io/openid/v1/jwks"}
				},
			),
		},
		{
			name: "When cloud provider config ref is provided, it should set cloud-config argument",
			params: KubeAPIServerConfigParams{
				CloudProviderConfigRef: &corev1.LocalObjectReference{
					Name: "foo",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["cloud-config"] = kcpv1.Arguments{"/etc/kubernetes/cloud"}
				},
			),
		},
		{
			name: "When unrecognized cloud provider is configured, it should set cloud-provider argument",
			params: KubeAPIServerConfigParams{
				CloudProvider: "alibaba",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["cloud-provider"] = kcpv1.Arguments{"alibaba"}
				},
			),
		},
		{
			name: "When audit webhook is enabled, it should configure audit webhook arguments",
			params: KubeAPIServerConfigParams{
				AuditWebhookEnabled: true,
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["audit-webhook-config-file"] = kcpv1.Arguments{"/etc/kubernetes/auditwebhook/webhook-kubeconfig"}
					kasc.APIServerArguments["audit-webhook-mode"] = kcpv1.Arguments{"batch"}
					kasc.APIServerArguments["audit-webhook-initial-backoff"] = kcpv1.Arguments{"5s"}
				},
			),
		},
		{
			name: "When profiling is disabled, it should set profiling argument to false",
			params: KubeAPIServerConfigParams{
				DisableProfiling: true,
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["profiling"] = kcpv1.Arguments{"false"}
				},
			),
		},
		{
			name: "When profiling is disabled, it should set profiling argument to false",
			params: KubeAPIServerConfigParams{
				DisableProfiling: true,
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["profiling"] = kcpv1.Arguments{"false"}
				},
			),
		},
		{
			name: "When OAuth is disabled, it should configure OIDC authentication",
			params: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
					OIDCProviders: []configv1.OIDCProvider{
						{},
					},
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["enable-admission-plugins"] = kcpv1.Arguments{
						"CertificateApproval",
						"CertificateSigning",
						"CertificateSubjectRestriction",
						"DefaultIngressClass",
						"DefaultStorageClass",
						"DefaultTolerationSeconds",
						"LimitRanger",
						"MutatingAdmissionWebhook",
						"NamespaceLifecycle",
						"NodeRestriction",
						"OwnerReferencesPermissionEnforcement",
						"PersistentVolumeClaimResize",
						"PodNodeSelector",
						"PodTolerationRestriction",
						"Priority",
						"ResourceQuota",
						"RuntimeClass",
						"ServiceAccount",
						"StorageObjectInUseProtection",
						"TaintNodesByCondition",
						"ValidatingAdmissionPolicy",
						"ValidatingAdmissionWebhook",
						"config.openshift.io/DenyDeleteClusterConfiguration",
						"config.openshift.io/ValidateAPIServer",
						"config.openshift.io/ValidateAuthentication",
						"config.openshift.io/ValidateConsole",
						"config.openshift.io/ValidateFeatureGate",
						"config.openshift.io/ValidateImage",
						"config.openshift.io/ValidateOAuth",
						"config.openshift.io/ValidateProject",
						"config.openshift.io/ValidateScheduler",
						"image.openshift.io/ImagePolicy",
						"network.openshift.io/ExternalIPRanger",
						"network.openshift.io/RestrictedEndpointsAdmission",
						"quota.openshift.io/ClusterResourceQuota",
						"quota.openshift.io/ValidateClusterResourceQuota",
						"route.openshift.io/IngressAdmission",
						"scheduling.openshift.io/OriginPodNodeEnvironment",
						"security.openshift.io/DefaultSecurityContextConstraints",
						"security.openshift.io/SCCExecRestrictions",
						"security.openshift.io/SecurityContextConstraint",
						"security.openshift.io/ValidateSecurityContextConstraints",
						"storage.openshift.io/CSIInlineVolumeSecurity",
					}

					kasc.APIServerArguments["disable-admission-plugins"] = kcpv1.Arguments{
						"authorization.openshift.io/RestrictSubjectBindings",
						"authorization.openshift.io/ValidateRoleBindingRestriction",
					}

					delete(kasc.APIServerArguments, "authentication-token-webhook-config-file")
					delete(kasc.APIServerArguments, "authentication-token-webhook-version")
					kasc.APIServerArguments["authentication-config"] = kcpv1.Arguments{"/etc/kubernetes/auth/auth.json"}

					kasc.AuthConfig.OAuthMetadataFile = ""
				},
			),
		},
		{
			name: "When etcd URL is provided, it should set etcd-servers argument",
			params: KubeAPIServerConfigParams{
				EtcdURL: "https://etcd.io",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["etcd-servers"] = kcpv1.Arguments{"https://etcd.io"}
				},
			),
		},
		{
			name: "When goaway chance is configured, it should set goaway-chance argument",
			params: KubeAPIServerConfigParams{
				GoAwayChance: "something",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["goaway-chance"] = kcpv1.Arguments{"something"}
				},
			),
		},
		{
			name: "When max mutating requests in flight is configured, it should set the argument",
			params: KubeAPIServerConfigParams{
				MaxMutatingRequestsInflight: "20",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["max-mutating-requests-inflight"] = kcpv1.Arguments{"20"}
				},
			),
		},
		{
			name: "When max requests in flight is configured, it should set the argument",
			params: KubeAPIServerConfigParams{
				MaxRequestsInflight: "20",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["max-requests-inflight"] = kcpv1.Arguments{"20"}
				},
			),
		},
		{
			name: "When DynamicResourceAllocation feature gate is enabled, it should configure runtime-config",
			params: KubeAPIServerConfigParams{
				FeatureGates: []string{
					"DynamicResourceAllocation=true",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["runtime-config"] = append(kcpv1.Arguments{"resource.k8s.io/v1beta1=true"}, kasc.APIServerArguments["runtime-config"]...)
					kasc.APIServerArguments["feature-gates"] = append(kcpv1.Arguments{"DynamicResourceAllocation=true"}, kasc.APIServerArguments["feature-gates"]...)
				},
			),
		},
		{
			name: "When ValidatingAdmissionPolicy feature gate is explicitly enabled, it should return default config",
			params: KubeAPIServerConfigParams{
				FeatureGates: []string{
					"ValidatingAdmissionPolicy=true",
				},
			},
			// shouldn't be any different than the default configuration because this feature gate is enabled by default
			expected: defaultKASConfig(),
		},
		{
			name: "When ValidatingAdmissionPolicy feature gate is explicitly disabled, it should return default config",
			params: KubeAPIServerConfigParams{
				FeatureGates: []string{
					"ValidatingAdmissionPolicy=false",
				},
			},
			// shouldn't be any different than the default configuration because this feature gate is forced to be enabled
			expected: defaultKASConfig(),
		},
		{
			name: "When StructuredAuthenticationConfiguration feature gate is explicitly disabled, it should return default config",
			params: KubeAPIServerConfigParams{
				FeatureGates: []string{
					"StructuredAuthenticationConfiguration=false",
				},
			},
			// shouldn't be any different than the default configuration because this feature gate is forced to be enabled
			expected: defaultKASConfig(),
		},
		{
			name: "When strict transport security directive is configured, it should set the directive argument",
			params: KubeAPIServerConfigParams{
				APIServerSTSDirectives: "foo",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["strict-transport-security-directives"] = kcpv1.Arguments{"foo"}
				},
			),
		},
		{
			name: "When MutatingAdmissionPolicy feature gate is enabled, it should not add v1alpha1 runtime-config",
			params: KubeAPIServerConfigParams{
				FeatureGates: []string{
					"MutatingAdmissionPolicy=true",
				},
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["feature-gates"] = append(kcpv1.Arguments{"MutatingAdmissionPolicy=true"}, kasc.APIServerArguments["feature-gates"]...)
				},
			),
		},
		{
			name: "When service account max token expiration is configured, it should set the argument",
			params: KubeAPIServerConfigParams{
				ServiceAccountMaxTokenExpiration: "24h",
			},
			expected: modifyKasConfig(defaultKASConfig(),
				func(kasc *kcpv1.KubeAPIServerConfig) {
					kasc.APIServerArguments["service-account-max-token-expiration"] = kcpv1.Arguments{"24h"}
				},
			),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kasConfig, _ := generateConfig(tc.params)

			diff := cmp.Diff(tc.expected, kasConfig)
			require.Empty(t, diff, "expected KAS config does not match actual")
		})
	}
}

func defaultKASConfig() *kcpv1.KubeAPIServerConfig {
	return &kcpv1.KubeAPIServerConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kubecontrolplane.config.openshift.io/v1",
			Kind:       "KubeAPIServerConfig",
		},
		GenericAPIServerConfig: configv1.GenericAPIServerConfig{
			ServingInfo: configv1.HTTPServingInfo{
				ServingInfo: configv1.ServingInfo{
					BindAddress: "0.0.0.0:0",
					BindNetwork: "tcp4",
					CertInfo: configv1.CertInfo{
						CertFile: "/etc/kubernetes/certs/server/tls.crt",
						KeyFile:  "/etc/kubernetes/certs/server/tls.key",
					},
					NamedCertificates: []configv1.NamedCertificate{
						{
							Names: []string{},
							CertInfo: configv1.CertInfo{
								CertFile: "/etc/kubernetes/certs/server-private/tls.crt",
								KeyFile:  "/etc/kubernetes/certs/server-private/tls.key",
							},
						},
					},
					MinTLSVersion: string(configv1.VersionTLS12),
					CipherSuites: []string{
						"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
						"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
						"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
						"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
						"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
						"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
					},
				},
			},
			CORSAllowedOrigins: []string{
				`//127\.0\.0\.1(:|$)`,
				"//localhost(:|$)",
			},
			AdmissionConfig: configv1.AdmissionConfig{
				PluginConfig: map[string]configv1.AdmissionPluginConfig{
					"PodSecurity": {
						Configuration: runtime.RawExtension{
							Object: &podsecurityadmissionv1.PodSecurityConfiguration{
								TypeMeta: metav1.TypeMeta{
									APIVersion: podsecurityadmissionv1.SchemeGroupVersion.String(),
									Kind:       "PodSecurityConfiguration",
								},
								Defaults: podsecurityadmissionv1.PodSecurityDefaults{
									Enforce:        "privileged",
									EnforceVersion: "latest",
									Audit:          "restricted",
									AuditVersion:   "latest",
									Warn:           "restricted",
									WarnVersion:    "latest",
								},
								Exemptions: podsecurityadmissionv1.PodSecurityExemptions{
									Usernames: []string{
										"system:serviceaccount:openshift-infra:build-controller",
									},
								},
							},
						},
					},
					"network.openshift.io/RestrictedEndpointsAdmission": {
						Location: "",
						Configuration: runtime.RawExtension{
							Object: &unstructured.Unstructured{
								Object: map[string]interface{}{
									"apiVersion":      "network.openshift.io/v1",
									"kind":            "RestrictedEndpointsAdmissionConfig",
									"restrictedCIDRs": []any{},
								},
							},
						},
					},
					"network.openshift.io/ExternalIPRanger": {
						Location: "",
						Configuration: runtime.RawExtension{
							Object: &unstructured.Unstructured{
								Object: map[string]interface{}{
									"apiVersion":             "network.openshift.io/v1",
									"kind":                   "ExternalIPRangerAdmissionConfig",
									"externalIPNetworkCIDRs": []any{},
									"allowIngressIP":         false,
								},
							},
						},
					},
				},
			},
		},
		AuthConfig: kcpv1.MasterAuthConfig{
			OAuthMetadataFile: "/etc/kubernetes/oauth/oauthMetadata.json",
		},
		ServiceAccountPublicKeyFiles: []string{
			"/etc/kubernetes/secrets/svcacct-key/service-account.pub",
		},
		APIServerArguments: map[string]kcpv1.Arguments{
			"advertise-address":                        {""},
			"allow-privileged":                         {"true"},
			"anonymous-auth":                           {"true"},
			"api-audiences":                            {""},
			"audit-log-format":                         {"json"},
			"audit-log-maxbackup":                      {"1"},
			"audit-log-maxsize":                        {"10"},
			"audit-log-path":                           {"/var/log/kube-apiserver/audit.log"},
			"audit-policy-file":                        {"/etc/kubernetes/audit/policy.yaml"},
			"authentication-token-webhook-config-file": {"/etc/kubernetes/auth-token-webhook/kubeconfig"},
			"authentication-token-webhook-version":     {"v1"},
			"authorization-mode": {
				"Scope",
				"SystemMasters",
				"RBAC",
				"Node",
			},
			"client-ca-file":              {"/etc/kubernetes/certs/client-ca/ca.crt"},
			"disable-admission-plugins":   {},
			"egress-selector-config-file": {"/etc/kubernetes/egress-selector/config.yaml"},
			"enable-admission-plugins": {
				"CertificateApproval",
				"CertificateSigning",
				"CertificateSubjectRestriction",
				"DefaultIngressClass",
				"DefaultStorageClass",
				"DefaultTolerationSeconds",
				"LimitRanger",
				"MutatingAdmissionWebhook",
				"NamespaceLifecycle",
				"NodeRestriction",
				"OwnerReferencesPermissionEnforcement",
				"PersistentVolumeClaimResize",
				"PodNodeSelector",
				"PodTolerationRestriction",
				"Priority",
				"ResourceQuota",
				"RuntimeClass",
				"ServiceAccount",
				"StorageObjectInUseProtection",
				"TaintNodesByCondition",
				"ValidatingAdmissionPolicy",
				"ValidatingAdmissionWebhook",
				"config.openshift.io/DenyDeleteClusterConfiguration",
				"config.openshift.io/ValidateAPIServer",
				"config.openshift.io/ValidateAuthentication",
				"config.openshift.io/ValidateConsole",
				"config.openshift.io/ValidateFeatureGate",
				"config.openshift.io/ValidateImage",
				"config.openshift.io/ValidateOAuth",
				"config.openshift.io/ValidateProject",
				"config.openshift.io/ValidateScheduler",
				"image.openshift.io/ImagePolicy",
				"network.openshift.io/ExternalIPRanger",
				"network.openshift.io/RestrictedEndpointsAdmission",
				"quota.openshift.io/ClusterResourceQuota",
				"quota.openshift.io/ValidateClusterResourceQuota",
				"route.openshift.io/IngressAdmission",
				"scheduling.openshift.io/OriginPodNodeEnvironment",
				"security.openshift.io/DefaultSecurityContextConstraints",
				"security.openshift.io/SCCExecRestrictions",
				"security.openshift.io/SecurityContextConstraint",
				"security.openshift.io/ValidateSecurityContextConstraints",
				"storage.openshift.io/CSIInlineVolumeSecurity",
				"authorization.openshift.io/RestrictSubjectBindings",
				"authorization.openshift.io/ValidateRoleBindingRestriction",
			},
			"enable-aggregator-routing": {"true"},
			"enable-logs-handler":       {"false"},
			"endpoint-reconciler-type":  {"none"},
			"etcd-cafile":               {"/etc/kubernetes/certs/etcd-ca/ca.crt"},
			"etcd-certfile":             {"/etc/kubernetes/certs/etcd/etcd-client.crt"},
			"etcd-keyfile":              {"/etc/kubernetes/certs/etcd/etcd-client.key"},
			"etcd-prefix":               {"kubernetes.io"},
			"etcd-servers":              {""},
			"event-ttl":                 {"3h"},
			"feature-gates": {
				"StructuredAuthenticationConfiguration=true",
				"ValidatingAdmissionPolicy=true",
			},
			"goaway-chance":                    {""},
			"http2-max-streams-per-connection": {"2000"},
			"kubelet-certificate-authority":    {"/etc/kubernetes/certs/kubelet-ca/ca.crt"},
			"kubelet-client-certificate":       {"/etc/kubernetes/certs/kubelet/tls.crt"},
			"kubelet-client-key":               {"/etc/kubernetes/certs/kubelet/tls.key"},
			"kubelet-preferred-address-types":  {"InternalIP"},
			"kubelet-read-only-port":           {"0"},
			"kubernetes-service-node-port":     {"0"},
			"max-mutating-requests-inflight":   {""},
			"max-requests-inflight":            {""},
			"min-request-timeout":              {"3600"},
			"proxy-client-cert-file":           {"/etc/kubernetes/certs/aggregator/tls.crt"},
			"proxy-client-key-file":            {"/etc/kubernetes/certs/aggregator/tls.key"},
			"requestheader-allowed-names": {
				"kube-apiserver-proxy",
				"system:kube-apiserver-proxy",
				"system:openshift-aggregator",
			},
			"requestheader-client-ca-file":            {"/etc/kubernetes/certs/aggregator-ca/ca.crt"},
			"requestheader-extra-headers-prefix":      {"X-Remote-Extra-"},
			"requestheader-group-headers":             {"X-Remote-Group"},
			"requestheader-username-headers":          {"X-Remote-User"},
			"runtime-config":                          {"admissionregistration.k8s.io/v1beta1=true"},
			"service-account-issuer":                  {""},
			"service-account-jwks-uri":                {"/openid/v1/jwks"},
			"service-account-lookup":                  {"true"},
			"service-account-signing-key-file":        {"/etc/kubernetes/secrets/svcacct-key/service-account.key"},
			"service-node-port-range":                 {""},
			"shutdown-delay-duration":                 {"70s"},
			"shutdown-send-retry-after":               {"true"},
			"shutdown-watch-termination-grace-period": {"25s"},
			"storage-backend":                         {"etcd3"},
			"storage-media-type":                      {"application/vnd.kubernetes.protobuf"},
			"strict-transport-security-directives":    {""},
			"tls-cert-file":                           {"/etc/kubernetes/certs/server/tls.crt"},
			"tls-private-key-file":                    {"/etc/kubernetes/certs/server/tls.key"},
		},
	}
}

func modifyKasConfig(in *kcpv1.KubeAPIServerConfig, modifications ...func(*kcpv1.KubeAPIServerConfig)) *kcpv1.KubeAPIServerConfig {
	for _, modification := range modifications {
		modification(in)
	}

	return in
}
