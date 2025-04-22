package ocm

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	buildv1 "github.com/openshift/api/build/v1"
	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConfigKey = "config.yaml"
)

func ReconcileOpenShiftControllerManagerConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, deployerImage, dockerBuilderImage, minTLSVersion string, cipherSuites []string, imageConfig *configv1.ImageSpec, buildConfig *configv1.Build, networkConfig *configv1.NetworkSpec, caps *hyperv1.Capabilities, featureGates []string) error {
	ownerRef.ApplyTo(cm)

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	if configStr, exists := cm.Data[ConfigKey]; exists && len(configStr) > 0 {
		err := util.DeserializeResource(configStr, config, api.Scheme)
		if err != nil {
			return fmt.Errorf("unable to decode existing openshift controller manager configuration: %w", err)
		}
	}
	if err := reconcileConfig(config, deployerImage, dockerBuilderImage, minTLSVersion,
		cipherSuites, imageConfig, buildConfig, networkConfig, caps, featureGates); err != nil {
		return err
	}
	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift controller manager configuration: %w", err)
	}
	cm.Data[ConfigKey] = configStr
	return nil
}

func reconcileConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, deployerImage, dockerBuilderImage, minTLSVersion string, cipherSuites []string, imageConfig *configv1.ImageSpec, buildConfig *configv1.Build, networkConfig *configv1.NetworkSpec, caps *hyperv1.Capabilities, featureGates []string) error {
	cpath := func(volume, file string) string {
		dir := volumeMounts.Path(ocmContainerMain().Name, volume)
		return path.Join(dir, file)
	}
	cfg.TypeMeta = metav1.TypeMeta{
		Kind:       "OpenShiftControllerManagerConfig",
		APIVersion: openshiftcpv1.GroupVersion.String(),
	}

	if !capabilities.IsImageRegistryCapabilityEnabled(caps) {
		cfg.Controllers = []string{"*", fmt.Sprintf("-%s", openshiftcpv1.OpenShiftServiceAccountPullSecretsController)}
	}

	cfg.Build.ImageTemplateFormat.Format = dockerBuilderImage
	cfg.Deployer.ImageTemplateFormat.Format = deployerImage

	// registry config
	if capabilities.IsImageRegistryCapabilityEnabled(caps) {
		cfg.DockerPullSecret.InternalRegistryHostname = config.DefaultImageRegistryHostname
	}

	if imageConfig != nil {
		cfg.DockerPullSecret.RegistryURLs = imageConfig.ExternalRegistryHostnames
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
	} else {
		cfg.Build.BuildDefaults = nil
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
	} else {
		cfg.Build.BuildOverrides = nil
	}

	// network config
	if networkConfig != nil && networkConfig.ExternalIP != nil && len(networkConfig.ExternalIP.AutoAssignCIDRs) > 0 {
		cfg.Ingress.IngressIPNetworkCIDR = networkConfig.ExternalIP.AutoAssignCIDRs[0]
	} else {
		cfg.Ingress.IngressIPNetworkCIDR = ""
	}

	cfg.KubeClientConfig.KubeConfig = cpath(ocmVolumeKubeconfig().Name, kas.KubeconfigKey)
	cfg.ServingInfo = &configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			BindAddress: fmt.Sprintf("0.0.0.0:%d", servingPort),
			CertInfo: configv1.CertInfo{
				CertFile: cpath(ocmVolumeServingCert().Name, corev1.TLSCertKey),
				KeyFile:  cpath(ocmVolumeServingCert().Name, corev1.TLSPrivateKeyKey),
			},
			ClientCA:      cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey),
			MinTLSVersion: minTLSVersion,
			CipherSuites:  cipherSuites,
		},
	}

	if len(featureGates) > 0 {
		cfg.FeatureGates = featureGates
	}
	return nil
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
