package oapi

import (
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	openshiftAPIServerConfigKey     = "config.yaml"
	configNamespace                 = "openshift-config"
	defaultInternalRegistryHostname = "image-registry.openshift-image-registry.svc:5000"
)

func ReconcileConfig(cm *corev1.ConfigMap, auditEnabled bool, auditWebhookRef *corev1.LocalObjectReference, ownerRef config.OwnerRef, etcdURL, ingressDomain, minTLSVersion string, cipherSuites []string, imageConfig *configv1.ImageSpec, projectConfig *configv1.Project) error {
	ownerRef.ApplyTo(cm)
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	openshiftAPIServerConfig := &openshiftcpv1.OpenShiftAPIServerConfig{}
	if configStr, exist := cm.Data[openshiftAPIServerConfigKey]; exist {
		if err := util.DeserializeResource(configStr, openshiftAPIServerConfig, api.Scheme); err != nil {
			return fmt.Errorf("failed to read existing config: %w", err)
		}
	}
	reconcileConfigObject(openshiftAPIServerConfig, auditEnabled, auditWebhookRef, etcdURL, ingressDomain, minTLSVersion, cipherSuites, imageConfig, projectConfig)
	serializedConfig, err := util.SerializeResource(openshiftAPIServerConfig, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift apiserver config: %w", err)
	}
	cm.Data[openshiftAPIServerConfigKey] = string(serializedConfig)
	return nil
}

func reconcileConfigObject(cfg *openshiftcpv1.OpenShiftAPIServerConfig, auditEnabled bool, auditWebhookRef *corev1.LocalObjectReference, etcdURL, ingressDomain, minTLSVersion string, cipherSuites []string, imageConfig *configv1.ImageSpec, projectConfig *configv1.Project) {
	cfg.TypeMeta = metav1.TypeMeta{
		Kind:       "OpenShiftAPIServerConfig",
		APIVersion: openshiftcpv1.GroupVersion.String(),
	}
	cpath := func(volume, file string) string {
		dir := volumeMounts.Path(oasContainerMain().Name, volume)
		return path.Join(dir, file)
	}
	cfg.APIServerArguments = map[string][]string{
		"shutdown-delay-duration": {"15s"},
	}

	if auditEnabled {
		cfg.APIServerArguments["audit-log-format"] = []string{"json"}
		cfg.APIServerArguments["audit-log-maxsize"] = []string{"10"}
		cfg.APIServerArguments["audit-log-maxbackup"] = []string{"1"}
		cfg.APIServerArguments["audit-policy-file"] = []string{path.Join(oasAuditConfigFileVolumeMount.Path(oasContainerMain().Name, oasVolumeAuditConfig().Name), auditPolicyConfigMapKey)}
		cfg.APIServerArguments["audit-log-path"] = []string{cpath(oasVolumeWorkLogs().Name, "audit.log")}
	}

	if auditWebhookRef != nil {
		cfg.APIServerArguments["audit-webhook-config-file"] = []string{auditWebhookConfigFile()}
		cfg.APIServerArguments["audit-webhook-mode"] = []string{"batch"}
	}

	cfg.KubeClientConfig.KubeConfig = cpath(oasVolumeKubeconfig().Name, kas.KubeconfigKey)
	cfg.ServingInfo = configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			CertInfo: configv1.CertInfo{
				CertFile: cpath(oasVolumeServingCert().Name, corev1.TLSCertKey),
				KeyFile:  cpath(oasVolumeServingCert().Name, corev1.TLSPrivateKeyKey),
			},
			ClientCA:      cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey),
			CipherSuites:  cipherSuites,
			MinTLSVersion: minTLSVersion,
		},
	}

	// Image policy config
	cfg.ImagePolicyConfig.InternalRegistryHostname = defaultInternalRegistryHostname
	if imageConfig != nil {
		cfg.ImagePolicyConfig.ExternalRegistryHostnames = imageConfig.ExternalRegistryHostnames
		var allowedRegistries openshiftcpv1.AllowedRegistries
		for _, location := range imageConfig.AllowedRegistriesForImport {
			allowedRegistries = append(allowedRegistries, openshiftcpv1.RegistryLocation{
				DomainName: location.DomainName,
				Insecure:   location.Insecure,
			})
		}
		cfg.ImagePolicyConfig.AllowedRegistriesForImport = allowedRegistries
	}

	// Routing config
	cfg.RoutingConfig.Subdomain = ingressDomain

	// Project config
	cfg.ProjectConfig.ProjectRequestMessage = projectConfig.Spec.ProjectRequestMessage
	if len(projectConfig.Spec.ProjectRequestTemplate.Name) > 0 {
		cfg.ProjectConfig.ProjectRequestTemplate = configNamespace + "/" + projectConfig.Spec.ProjectRequestTemplate.Name
	} else {
		cfg.ProjectConfig.ProjectRequestTemplate = ""
	}

	cfg.StorageConfig = configv1.EtcdStorageConfig{
		EtcdConnectionInfo: configv1.EtcdConnectionInfo{
			URLs: []string{etcdURL},
			CertInfo: configv1.CertInfo{
				CertFile: cpath(oasVolumeEtcdClientCert().Name, pki.EtcdClientCrtKey),
				KeyFile:  cpath(oasVolumeEtcdClientCert().Name, pki.EtcdClientKeyKey),
			},
			CA: cpath(oasVolumeEtcdClientCA().Name, certs.CASignerCertMapKey),
		},
	}
}

func auditWebhookConfigFile() string {
	cfgDir := oasAuditWebhookConfigFileVolumeMount.Path(oasContainerMain().Name, oasAuditWebhookConfigFileVolume().Name)
	return path.Join(cfgDir, hyperv1.AuditWebhookKubeconfigKey)
}
