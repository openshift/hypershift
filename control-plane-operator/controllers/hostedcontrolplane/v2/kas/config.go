package kas

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"
	hcpconfig "github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	podsecurityadmissionv1 "k8s.io/pod-security-admission/admission/api/v1"
)

const (
	KubeAPIServerConfigKey  = "config.json"
	AuthenticationConfigKey = "auth.json"
	OauthMetadataConfigKey  = "oauthMetadata.json"
	AuditLogFile            = "audit.log"
	EgressSelectorConfigKey = "config.yaml"
	DefaultEtcdPort         = 2379
)

func adaptKubeAPIServerConfig(cpContext component.WorkloadContext, config *corev1.ConfigMap) error {
	featureGates, err := hcpconfig.FeatureGatesFromConfigMap(cpContext.Context, cpContext.Client, cpContext.HCP.Namespace)
	if err != nil {
		return err
	}
	configParams := NewConfigParams(cpContext.HCP, featureGates)
	kasConfig, err := generateConfig(configParams)
	if err != nil {
		return err
	}
	serializedConfig, err := json.Marshal(kasConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize kube apiserver config: %w", err)
	}

	if config.Data == nil {
		config.Data = map[string]string{}
	}
	config.Data[KubeAPIServerConfigKey] = string(serializedConfig)
	return nil
}

type kubeAPIServerArgs map[string]kcpv1.Arguments

func (a kubeAPIServerArgs) Set(name string, values ...string) {
	v := kcpv1.Arguments{}
	v = append(v, values...)
	a[name] = v
}

func generateConfig(p KubeAPIServerConfigParams) (*kcpv1.KubeAPIServerConfig, error) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(ComponentName, volume), file)
	}
	namedCertificates := globalconfig.GetConfigNamedCertificates(p.NamedCertificates, kasNamedCertificateMountPathPrefix)
	namedCertificates = append(namedCertificates, configv1.NamedCertificate{
		Names: []string{},
		CertInfo: configv1.CertInfo{
			CertFile: cpath(serverPrivateCertVolumeName, corev1.TLSCertKey),
			KeyFile:  cpath(serverPrivateCertVolumeName, corev1.TLSPrivateKeyKey),
		},
	})
	externalIPRangeConfig, err := externalIPRangerConfig(p.ExternalIPConfig)
	if err != nil {
		return nil, err
	}
	restrictedEndpointsAdmissionConfig, err := restrictedEndpointsAdmission(p.ClusterNetwork, p.ServiceNetwork)
	if err != nil {
		return nil, err
	}
	config := &kcpv1.KubeAPIServerConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeAPIServerConfig",
			APIVersion: kcpv1.GroupVersion.String(),
		},
		GenericAPIServerConfig: configv1.GenericAPIServerConfig{
			AdmissionConfig: configv1.AdmissionConfig{
				PluginConfig: map[string]configv1.AdmissionPluginConfig{
					"network.openshift.io/ExternalIPRanger": {
						Location: "",
						Configuration: runtime.RawExtension{
							Object: externalIPRangeConfig,
						},
					},
					"network.openshift.io/RestrictedEndpointsAdmission": {
						Location: "",
						Configuration: runtime.RawExtension{
							Object: restrictedEndpointsAdmissionConfig,
						},
					},
					"PodSecurity": {
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
					},
				},
			},
			ServingInfo: configv1.HTTPServingInfo{
				ServingInfo: configv1.ServingInfo{
					CertInfo: configv1.CertInfo{
						CertFile: cpath(serverCertVolumeName, corev1.TLSCertKey),
						KeyFile:  cpath(serverCertVolumeName, corev1.TLSPrivateKeyKey),
					},
					NamedCertificates: namedCertificates,
					BindAddress:       fmt.Sprintf("0.0.0.0:%d", p.KASPodPort),
					BindNetwork:       "tcp4",
					CipherSuites:      hcpconfig.CipherSuites(p.TLSSecurityProfile),
					MinTLSVersion:     hcpconfig.MinTLSVersion(p.TLSSecurityProfile),
				},
			},
			CORSAllowedOrigins: corsAllowedOrigins(p.AdditionalCORSAllowedOrigins),
		},
		ConsolePublicURL:             p.ConsolePublicURL,
		ImagePolicyConfig:            imagePolicyConfig(p.InternalRegistryHostName, p.ExternalRegistryHostNames),
		ProjectConfig:                projectConfig(p.DefaultNodeSelector),
		ServiceAccountPublicKeyFiles: []string{cpath(serviceAccountKeyVolumeName, pki.ServiceSignerPublicKey)},
		ServicesSubnet:               strings.Join(p.ServiceNetwork, ","),
	}

	if !slices.Contains(p.FeatureGates, "OpenShiftPodSecurityAdmission=true") {
		config.AdmissionConfig.PluginConfig["PodSecurity"].Configuration.Object.(*podsecurityadmissionv1.PodSecurityConfiguration).Defaults.Enforce = "privileged"
	} else {
		config.AdmissionConfig.PluginConfig["PodSecurity"].Configuration.Object.(*podsecurityadmissionv1.PodSecurityConfiguration).Defaults.Enforce = "restricted"
	}

	if p.Authentication == nil || p.Authentication.Type == configv1.AuthenticationTypeIntegratedOAuth {
		config.AuthConfig.OAuthMetadataFile = cpath(oauthMetadataVolumeName, OauthMetadataConfigKey)
	}

	args := kubeAPIServerArgs{}
	args.Set("advertise-address", p.AdvertiseAddress)
	args.Set("allow-privileged", "true")
	args.Set("anonymous-auth", "true")
	args.Set("api-audiences", p.ServiceAccountIssuerURL)
	args.Set("audit-log-format", "json")
	args.Set("audit-log-maxbackup", "1")
	args.Set("audit-log-maxsize", "10")
	args.Set("audit-log-path", cpath(workLogsVolumeName, AuditLogFile))
	args.Set("audit-policy-file", cpath(auditConfigVolumeName, AuditPolicyConfigMapKey))
	args.Set("authorization-mode", "Scope", "SystemMasters", "RBAC", "Node")
	args.Set("client-ca-file", cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey))
	if p.CloudProviderConfigRef != nil && p.CloudProvider != azure.Provider {
		args.Set("cloud-config", cloudProviderConfig(p.CloudProviderConfigRef.Name, p.CloudProvider))
	}
	if p.CloudProvider != "" && p.CloudProvider != aws.Provider && p.CloudProvider != azure.Provider && p.CloudProvider != openstack.Provider {
		args.Set("cloud-provider", p.CloudProvider)
	}
	if p.AuditWebhookEnabled {
		args.Set("audit-webhook-config-file", auditWebhookConfigFile())
		args.Set("audit-webhook-mode", "batch")
		args.Set("audit-webhook-initial-backoff", "5s")
	}
	if p.DisableProfiling {
		args.Set("profiling", "false")
	}
	args.Set("egress-selector-config-file", cpath(egressSelectorConfigVolumeName, EgressSelectorConfigKey))
	args.Set("enable-admission-plugins", enabledAdmissionPlugins(p)...)
	args.Set("disable-admission-plugins", disabledAdmissionPlugins(p)...)
	if util.ConfigOAuthEnabled(p.Authentication) {
		args.Set("authentication-token-webhook-config-file", cpath(authTokenWebhookConfigVolumeName, KubeconfigKey))
		args.Set("authentication-token-webhook-version", "v1")
	} else {
		if p.Authentication != nil && len(p.Authentication.OIDCProviders) > 0 {
			args.Set("authentication-config", cpath(authConfigVolumeName, AuthenticationConfigKey))
		}
	}
	args.Set("enable-aggregator-routing", "true")
	args.Set("enable-logs-handler", "false")
	args.Set("endpoint-reconciler-type", "none")
	args.Set("etcd-cafile", cpath(etcdCAVolumeName, certs.CASignerCertMapKey))
	args.Set("etcd-certfile", cpath(etcdClientCertVolumeName, pki.EtcdClientCrtKey))
	args.Set("etcd-keyfile", cpath(etcdClientCertVolumeName, pki.EtcdClientKeyKey))
	args.Set("etcd-prefix", "kubernetes.io")
	args.Set("etcd-servers", p.EtcdURL)
	args.Set("event-ttl", "3h")
	// TODO remove in 4.16 once we're able to have different featuregates for hypershift
	featureGates := append([]string{}, p.FeatureGates...)
	featureGates = enforceFeatureGates(featureGates, "ValidatingAdmissionPolicy=true", "StructuredAuthenticationConfiguration=true")
	args.Set("feature-gates", featureGates...)
	args.Set("goaway-chance", p.GoAwayChance)
	args.Set("http2-max-streams-per-connection", "2000")
	args.Set("kubelet-certificate-authority", cpath(kubeletClientCAVolumeName, certs.CASignerCertMapKey))
	args.Set("kubelet-client-certificate", cpath(kubeletClientCertVolumeName, corev1.TLSCertKey))
	args.Set("kubelet-client-key", cpath(kubeletClientCertVolumeName, corev1.TLSPrivateKeyKey))
	args.Set("kubelet-preferred-address-types", "InternalIP")
	args.Set("kubelet-read-only-port", "0")
	args.Set("kubernetes-service-node-port", "0")
	args.Set("max-mutating-requests-inflight", p.MaxMutatingRequestsInflight)
	args.Set("max-requests-inflight", p.MaxRequestsInflight)
	args.Set("min-request-timeout", "3600")
	args.Set("proxy-client-cert-file", cpath(aggregatorCertVolumeName, corev1.TLSCertKey))
	args.Set("proxy-client-key-file", cpath(aggregatorCertVolumeName, corev1.TLSPrivateKeyKey))
	args.Set("requestheader-allowed-names", requestHeaderAllowedNames()...)
	args.Set("requestheader-client-ca-file", cpath(common.VolumeAggregatorCA().Name, certs.CASignerCertMapKey))
	args.Set("requestheader-extra-headers-prefix", "X-Remote-Extra-")
	args.Set("requestheader-group-headers", "X-Remote-Group")
	args.Set("requestheader-username-headers", "X-Remote-User")
	runtimeConfig := []string{}
	for _, gate := range featureGates {
		if gate == "ValidatingAdmissionPolicy=true" {
			runtimeConfig = append(runtimeConfig, "admissionregistration.k8s.io/v1beta1=true")
		}
		if gate == "DynamicResourceAllocation=true" {
			runtimeConfig = append(runtimeConfig, "resource.k8s.io/v1beta1=true")
		}
	}
	args.Set("runtime-config", runtimeConfig...)
	args.Set("service-account-issuer", p.ServiceAccountIssuerURL)
	args.Set("service-account-jwks-uri", jwksURL(p.ServiceAccountIssuerURL))
	args.Set("service-account-lookup", "true")
	args.Set("service-account-signing-key-file", cpath(serviceAccountKeyVolumeName, pki.ServiceSignerPrivateKey))
	args.Set("service-node-port-range", p.NodePortRange)
	args.Set("shutdown-delay-duration", "70s")
	args.Set("shutdown-watch-termination-grace-period", "25s")
	args.Set("shutdown-send-retry-after", "true")
	args.Set("storage-backend", "etcd3")
	args.Set("storage-media-type", "application/vnd.kubernetes.protobuf")
	args.Set("strict-transport-security-directives", p.APIServerSTSDirectives)
	args.Set("tls-cert-file", cpath(serverCertVolumeName, corev1.TLSCertKey))
	args.Set("tls-private-key-file", cpath(serverCertVolumeName, corev1.TLSPrivateKeyKey))
	config.APIServerArguments = args
	return config, nil
}

func cloudProviderConfig(cloudProviderConfigName, cloudProvider string) string {
	if cloudProviderConfigName != "" {
		cfgDir := cloudProviderConfigVolumeMount.Path(ComponentName, cloudConfigVolumeName)
		return path.Join(cfgDir, cloud.ProviderConfigKey(cloudProvider))
	}
	return ""
}

func externalIPRangerConfig(externalIPConfig *configv1.ExternalIPConfig) (runtime.Object, error) {
	cfg := &unstructured.Unstructured{}
	cfg.SetAPIVersion("network.openshift.io/v1")
	cfg.SetKind("ExternalIPRangerAdmissionConfig")
	conf := []string{}
	if externalIPConfig != nil && externalIPConfig.Policy != nil {
		for _, cidr := range externalIPConfig.Policy.RejectedCIDRs {
			conf = append(conf, "!"+cidr)
		}
		conf = append(conf, externalIPConfig.Policy.AllowedCIDRs...)
	}
	err := unstructured.SetNestedStringSlice(cfg.Object, conf, "externalIPNetworkCIDRs")
	if err != nil {
		return nil, err
	}
	allowIngressIP := externalIPConfig != nil && len(externalIPConfig.AutoAssignCIDRs) > 0
	err = unstructured.SetNestedField(cfg.Object, allowIngressIP, "allowIngressIP")
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func restrictedEndpointsAdmission(clusterNetwork, serviceNetwork []string) (runtime.Object, error) {
	cfg := &unstructured.Unstructured{}
	cfg.SetAPIVersion("network.openshift.io/v1")
	cfg.SetKind("RestrictedEndpointsAdmissionConfig")
	var restrictedCIDRs []string
	restrictedCIDRs = append(restrictedCIDRs, clusterNetwork...)
	restrictedCIDRs = append(restrictedCIDRs, serviceNetwork...)
	err := unstructured.SetNestedStringSlice(cfg.Object, restrictedCIDRs, "restrictedCIDRs")
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func enabledAdmissionPlugins(cfg KubeAPIServerConfigParams) []string {
	enabled := []string{
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

	if util.ConfigOAuthEnabled(cfg.Authentication) {
		enabled = append(enabled, "authorization.openshift.io/RestrictSubjectBindings", "authorization.openshift.io/ValidateRoleBindingRestriction")
	}

	return enabled
}

func disabledAdmissionPlugins(cfg KubeAPIServerConfigParams) []string {
	disabled := []string{}

	if !util.ConfigOAuthEnabled(cfg.Authentication) {
		disabled = append(disabled, "authorization.openshift.io/RestrictSubjectBindings", "authorization.openshift.io/ValidateRoleBindingRestriction")
	}

	return disabled
}

func corsAllowedOrigins(additionalCORSAllowedOrigins []string) []string {
	corsAllowed := []string{
		"//127\\.0\\.0\\.1(:|$)",
		"//localhost(:|$)",
	}
	corsAllowed = append(corsAllowed, additionalCORSAllowedOrigins...)
	return corsAllowed
}

func imagePolicyConfig(internalRegistryHostname string, externalRegistryHostnames []string) kcpv1.KubeAPIServerImagePolicyConfig {
	cfg := kcpv1.KubeAPIServerImagePolicyConfig{
		InternalRegistryHostname:  internalRegistryHostname,
		ExternalRegistryHostnames: externalRegistryHostnames,
	}
	return cfg
}

func projectConfig(defaultNodeSelector string) kcpv1.KubeAPIServerProjectConfig {
	return kcpv1.KubeAPIServerProjectConfig{
		DefaultNodeSelector: defaultNodeSelector,
	}
}

func requestHeaderAllowedNames() []string {
	return []string{
		"kube-apiserver-proxy",
		"system:kube-apiserver-proxy",
		"system:openshift-aggregator",
	}
}

func jwksURL(issuerURL string) string {
	return fmt.Sprintf("%s/openid/v1/jwks", issuerURL)
}

func auditWebhookConfigFile() string {
	cfgDir := kasAuditWebhookConfigFileVolumeMount.Path(ComponentName, auditWebhookConfigFileVolumeName)
	return path.Join(cfgDir, hyperv1.AuditWebhookKubeconfigKey)
}

// enforceFeatureGates is a helper function that ensures the feature gates
// are set to a particular state based on the provided feature gate strings.
// If the existing set of feature gate strings does not contain a
// desired feature gate string it is added.
// If the existing set of feature gate strings does contain a desired feature gate,
// but sets the state to a different value, it will be overridden to match
// the desired state.
func enforceFeatureGates(featureGates []string, enforced ...string) []string {
	existingSet := featureGatesStringToMap(featureGates...)

	for _, gate := range enforced {
		key, state := keyAndStateForFeatureGateString(gate)
		existingSet[key] = state
	}

	return featureGateMapToSlice(existingSet)
}

// featureGatesStringToMap generates a map[string]string
// containing the "keys" and "states" of the feature gates strings.
// For example, a feature gate string of "Foo=true" would result in the
// key "Foo" being present in the map, pointing to the string "true".
func featureGatesStringToMap(gates ...string) map[string]string {
	gateMapping := map[string]string{}
	for _, gate := range gates {
		key, state := keyAndStateForFeatureGateString(gate)
		gateMapping[key] = state
	}

	return gateMapping
}

// keyAndStateForFeatureGateString returns the "key" and "state" of a feature gate string.
// For example, a feature gate string of "Foo=true" would result
// in the key "Foo" and state "true" being returned.
// All inputs are expected to be valid feature gate strings, meaning
// that they follow the pattern `{GateName}={true || false}`.
func keyAndStateForFeatureGateString(gate string) (string, string) {
	splits := strings.Split(gate, "=")
	return splits[0], splits[1]
}

func featureGateMapToSlice(gates map[string]string) []string {
	out := []string{}
	for gate, state := range gates {
		out = append(out, fmt.Sprintf("%s=%s", gate, state))
	}

	// sort the slice for deterministic ordering to prevent
	// potential for thrashing when generating configurations
	slices.Sort(out)

	return out
}
