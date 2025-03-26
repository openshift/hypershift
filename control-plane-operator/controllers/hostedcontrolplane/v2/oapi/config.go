package oapi

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	openshiftAPIServerConfigKey = "config.yaml"
	configNamespace             = "openshift-config"
)

func adaptConfigMap(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	if configStr, exists := cm.Data[openshiftAPIServerConfigKey]; !exists || len(configStr) == 0 {
		return fmt.Errorf("expected an existing openshift apiserver configuration")
	}

	openshiftAPIServerConfig := &openshiftcpv1.OpenShiftAPIServerConfig{}
	if err := util.DeserializeResource(cm.Data[openshiftAPIServerConfigKey], openshiftAPIServerConfig, api.Scheme); err != nil {
		return fmt.Errorf("failed to decode existing openshift apiserver configuration: %w", err)
	}

	observedConfig := &globalconfig.ObservedConfig{}
	if err := globalconfig.ReadObservedConfig(cpContext, cpContext.Client, observedConfig, cpContext.HCP.Namespace); err != nil {
		return fmt.Errorf("failed to read observed global config: %w", err)
	}

	adaptConfig(openshiftAPIServerConfig, cpContext.HCP, observedConfig.Project)
	serializedConfig, err := util.SerializeResource(openshiftAPIServerConfig, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift apiserver configuration: %w", err)
	}
	cm.Data[openshiftAPIServerConfigKey] = string(serializedConfig)
	return nil
}

func adaptConfig(cfg *openshiftcpv1.OpenShiftAPIServerConfig, hcp *hyperv1.HostedControlPlane, projectConfig *configv1.Project) {
	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		cfg.APIServerArguments["audit-webhook-config-file"] = []string{path.Join("/etc/kubernetes/auditwebhook", hyperv1.AuditWebhookKubeconfigKey)}
		cfg.APIServerArguments["audit-webhook-mode"] = []string{"batch"}
	}

	configuration := hcp.Spec.Configuration
	cfg.ServingInfo.MinTLSVersion = config.MinTLSVersion(configuration.GetTLSSecurityProfile())
	cfg.ServingInfo.CipherSuites = config.CipherSuites(configuration.GetTLSSecurityProfile())

	if configuration != nil && configuration.Image != nil {
		cfg.ImagePolicyConfig.ExternalRegistryHostnames = configuration.Image.ExternalRegistryHostnames
		var allowedRegistries openshiftcpv1.AllowedRegistries
		for _, location := range configuration.Image.AllowedRegistriesForImport {
			allowedRegistries = append(allowedRegistries, openshiftcpv1.RegistryLocation{
				DomainName: location.DomainName,
				Insecure:   location.Insecure,
			})
		}
		cfg.ImagePolicyConfig.AllowedRegistriesForImport = allowedRegistries
	}

	if !capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		cfg.ImagePolicyConfig.InternalRegistryHostname = ""
	}

	// Routing config
	cfg.RoutingConfig.Subdomain = globalconfig.IngressDomain(hcp)

	// Project config
	cfg.ProjectConfig.ProjectRequestMessage = projectConfig.Spec.ProjectRequestMessage
	if len(projectConfig.Spec.ProjectRequestTemplate.Name) > 0 {
		cfg.ProjectConfig.ProjectRequestTemplate = configNamespace + "/" + projectConfig.Spec.ProjectRequestTemplate.Name
	}

	if hcp.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		cfg.StorageConfig.EtcdConnectionInfo.URLs = []string{hcp.Spec.Etcd.Unmanaged.Endpoint}
	}
}
