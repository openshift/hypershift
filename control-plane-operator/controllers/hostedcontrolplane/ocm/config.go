package ocm

import (
	"bytes"
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	"github.com/openshift/hypershift/control-plane-operator/api"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
)

const (
	configKey = "config.yaml"
)

func ReconcileOpenShiftControllerManagerConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, deployerImage, dockerBuilderImage, minTLSVersion string, cipherSuites []string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	if configBytes, exists := cm.Data[configKey]; exists && len(configBytes) > 0 {
		_, _, err := api.YamlSerializer.Decode([]byte(configBytes), nil, config)
		if err != nil {
			return fmt.Errorf("unable to decode existing openshift controller manager configuration: %w", err)
		}
	}
	if err := reconcileConfig(config, deployerImage, dockerBuilderImage, minTLSVersion, cipherSuites); err != nil {
		return err
	}
	buf := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(config, buf); err != nil {
		return fmt.Errorf("failed to serialize openshift controller manager configuration: %w", err)
	}
	cm.Data[configKey] = buf.String()
	return nil
}

func reconcileConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, deployerImage, dockerBuilderImage, minTLSVersion string, cipherSuites []string) error {
	cpath := func(volume, file string) string {
		dir := volumeMounts.Path(ocmContainerMain().Name, volume)
		return path.Join(dir, file)
	}
	cfg.TypeMeta = metav1.TypeMeta{
		Kind:       "OpenShiftControllerManagerConfig",
		APIVersion: openshiftcpv1.GroupVersion.String(),
	}
	cfg.Build.ImageTemplateFormat.Format = dockerBuilderImage
	cfg.Deployer.ImageTemplateFormat.Format = deployerImage
	cfg.DockerPullSecret.InternalRegistryHostname = config.DefaultImageRegistryHostname
	cfg.KubeClientConfig.KubeConfig = cpath(ocmVolumeKubeconfig().Name, kas.KubeconfigKey)
	cfg.ServingInfo = &configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			CertInfo: configv1.CertInfo{
				CertFile: cpath(ocmVolumeServingCert().Name, corev1.TLSCertKey),
				KeyFile:  cpath(ocmVolumeServingCert().Name, corev1.TLSPrivateKeyKey),
			},
			ClientCA:      cpath(ocmVolumeServingCert().Name, pki.CASignerCertMapKey),
			MinTLSVersion: minTLSVersion,
			CipherSuites:  cipherSuites,
		},
	}
	return nil
}
