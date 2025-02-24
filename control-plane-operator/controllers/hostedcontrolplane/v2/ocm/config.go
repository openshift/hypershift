package ocm

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"

	buildv1 "github.com/openshift/api/build/v1"
	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	configKey = "config.yaml"
)

func adaptConfigMap(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	if configStr, exists := cm.Data[configKey]; !exists || len(configStr) == 0 {
		return fmt.Errorf("expected an existing openshift controller manager configuration")
	}

	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	err := util.DeserializeResource(cm.Data[configKey], config, api.Scheme)
	if err != nil {
		return fmt.Errorf("unable to decode existing openshift controller manager configuration: %w", err)
	}

	observedConfig := &globalconfig.ObservedConfig{}
	if err := globalconfig.ReadObservedConfig(cpContext, cpContext.Client, observedConfig, cpContext.HCP.Namespace); err != nil {
		return fmt.Errorf("failed to read observed global config: %w", err)
	}

	adaptConfig(config, cpContext.HCP.Spec.Configuration, cpContext.ReleaseImageProvider, observedConfig.Build, cpContext.HCP.Spec.Capabilities)
	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift controller manager configuration: %w", err)
	}
	cm.Data[configKey] = configStr
	return nil
}

func adaptConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, configuration *hyperv1.ClusterConfiguration, releaseImageProvider imageprovider.ReleaseImageProvider, buildConfig *configv1.Build, caps *hyperv1.Capabilities) {
	cfg.Build.ImageTemplateFormat.Format = releaseImageProvider.GetImage("docker-builder")
	cfg.Deployer.ImageTemplateFormat.Format = releaseImageProvider.GetImage("deployer")

	if !capabilities.IsImageRegistryCapabilityEnabled(caps) {
		cfg.Controllers = []string{"*", fmt.Sprintf("-%s", openshiftcpv1.OpenShiftServiceAccountPullSecretsController)}
		cfg.DockerPullSecret.InternalRegistryHostname = ""
	}

	if configuration != nil && configuration.Image != nil {
		cfg.DockerPullSecret.RegistryURLs = configuration.Image.ExternalRegistryHostnames
	}

	// build config
	if hasBuildDefaults(buildConfig) {
		cfg.Build.BuildDefaults = &openshiftcpv1.BuildDefaultsConfig{}
		if buildConfig.Spec.BuildDefaults.GitProxy != nil {
			cfg.Build.BuildDefaults.GitHTTPProxy = buildConfig.Spec.BuildDefaults.DefaultProxy.HTTPProxy
			cfg.Build.BuildDefaults.GitHTTPSProxy = buildConfig.Spec.BuildDefaults.DefaultProxy.HTTPSProxy
			cfg.Build.BuildDefaults.GitNoProxy = buildConfig.Spec.BuildDefaults.DefaultProxy.NoProxy
		}
		cfg.Build.BuildDefaults.Env = buildConfig.Spec.BuildDefaults.Env
		for _, label := range buildConfig.Spec.BuildDefaults.ImageLabels {
			cfg.Build.BuildDefaults.ImageLabels = append(cfg.Build.BuildDefaults.ImageLabels, buildv1.ImageLabel{
				Name:  label.Name,
				Value: label.Value,
			})
		}
		cfg.Build.BuildDefaults.Resources = buildConfig.Spec.BuildDefaults.Resources
	}

	if hasBuildOverrides(buildConfig) {
		cfg.Build.BuildOverrides = &openshiftcpv1.BuildOverridesConfig{}
		cfg.Build.BuildOverrides.ForcePull = buildConfig.Spec.BuildOverrides.ForcePull
		for _, label := range buildConfig.Spec.BuildOverrides.ImageLabels {
			cfg.Build.BuildOverrides.ImageLabels = append(cfg.Build.BuildOverrides.ImageLabels, buildv1.ImageLabel{
				Name:  label.Name,
				Value: label.Value,
			})
		}
		cfg.Build.BuildOverrides.NodeSelector = buildConfig.Spec.BuildOverrides.NodeSelector
		cfg.Build.BuildOverrides.Tolerations = buildConfig.Spec.BuildOverrides.Tolerations
	}

	// network config
	if cidrs := configuration.GetAutoAssignCIDRs(); len(cidrs) > 0 {
		cfg.Ingress.IngressIPNetworkCIDR = cidrs[0]
	}

	cfg.ServingInfo.MinTLSVersion = config.MinTLSVersion(configuration.GetTLSSecurityProfile())
	cfg.ServingInfo.CipherSuites = config.CipherSuites(configuration.GetTLSSecurityProfile())
}

func hasBuildDefaults(cfg *configv1.Build) bool {
	return cfg.Spec.BuildDefaults.GitProxy != nil ||
		len(cfg.Spec.BuildDefaults.Env) > 0 ||
		len(cfg.Spec.BuildDefaults.ImageLabels) > 0 ||
		len(cfg.Spec.BuildDefaults.Resources.Limits) > 0 ||
		len(cfg.Spec.BuildDefaults.Resources.Requests) > 0
}

func hasBuildOverrides(cfg *configv1.Build) bool {
	return len(cfg.Spec.BuildOverrides.ImageLabels) > 0 ||
		len(cfg.Spec.BuildOverrides.NodeSelector) > 0 ||
		len(cfg.Spec.BuildOverrides.Tolerations) > 0 ||
		cfg.Spec.BuildOverrides.ForcePull != nil
}
