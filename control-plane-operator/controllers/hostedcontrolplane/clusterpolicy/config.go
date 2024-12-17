package clusterpolicy

import (
	"bytes"
	"fmt"
	"path"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	configKey = "config.yaml"
	bindPort  = 10357
)

func ReconcileClusterPolicyControllerConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, minTLSVersion string, cipherSuites []string, featureGates []string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	if configBytes, exists := cm.Data[configKey]; exists && len(configBytes) > 0 {
		_, _, err := api.YamlSerializer.Decode([]byte(configBytes), nil, config)
		if err != nil {
			return fmt.Errorf("unable to decode existing cluster policy controller configuration: %w", err)
		}
	}
	if err := reconcileConfig(config, minTLSVersion, cipherSuites, featureGates); err != nil {
		return err
	}
	buf := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(config, buf); err != nil {
		return fmt.Errorf("failed to serialize cluster policy controller configuration: %w", err)
	}
	cm.Data[configKey] = buf.String()
	return nil
}

func reconcileConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, minTLSVersion string, cipherSuites []string, featureGates []string) error {
	cpath := func(volume, file string) string {
		dir := volumeMounts.Path(cpcContainerMain().Name, volume)
		return path.Join(dir, file)
	}
	cfg.TypeMeta = metav1.TypeMeta{
		Kind:       "OpenShiftControllerManagerConfig",
		APIVersion: openshiftcpv1.GroupVersion.String(),
	}
	cfg.KubeClientConfig.KubeConfig = cpath(cpcVolumeKubeconfig().Name, kas.KubeconfigKey)
	cfg.ServingInfo = &configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			BindAddress: fmt.Sprintf("0.0.0.0:%d", bindPort),
			BindNetwork: "tcp",
			CertInfo: configv1.CertInfo{
				CertFile: cpath(cpcVolumeServingCert().Name, corev1.TLSCertKey),
				KeyFile:  cpath(cpcVolumeServingCert().Name, corev1.TLSPrivateKeyKey),
			},
			ClientCA:      cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey),
			MinTLSVersion: minTLSVersion,
			CipherSuites:  cipherSuites,
		},
	}

	cfg.FeatureGates = featureGates

	return nil
}
