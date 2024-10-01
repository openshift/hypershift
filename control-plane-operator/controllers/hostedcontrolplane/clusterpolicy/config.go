package clusterpolicy

import (
	"bytes"
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
)

const (
	configKey = "config.yaml"
	bindPort  = 10357
)

func ReconcileClusterPolicyControllerConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, minTLSVersion string, cipherSuites []string) error {
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
	if err := reconcileConfig(config, minTLSVersion, cipherSuites); err != nil {
		return err
	}
	buf := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(config, buf); err != nil {
		return fmt.Errorf("failed to serialize cluster policy controller configuration: %w", err)
	}
	cm.Data[configKey] = buf.String()
	return nil
}

func reconcileConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, minTLSVersion string, cipherSuites []string) error {
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

	// disables automatically setting the `pod-security.kubernetes.io/enforce` label on namespaces by the pod-security-admission-label-synchronization-controller
	// see https://github.com/openshift/cluster-policy-controller/blob/50c2a8337f08856bbae4cd419bb8ffcbdf92567c/pkg/cmd/controller/psalabelsyncer.go#L19
	index := -1
	for i := range cfg.FeatureGates {
		fg := cfg.FeatureGates[i]
		if strings.HasPrefix(fg, "OpenShiftPodSecurityAdmission") {
			index = i
			break
		}
	}

	if index != -1 {
		// overwrite
		cfg.FeatureGates[index] = "OpenShiftPodSecurityAdmission=false"
	} else {
		cfg.FeatureGates = append(cfg.FeatureGates, "OpenShiftPodSecurityAdmission=false")
	}

	return nil
}
