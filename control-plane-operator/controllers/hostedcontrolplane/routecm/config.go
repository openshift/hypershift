package routecm

import (
	"fmt"
	"path"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	configKey = "config.yaml"
)

func ReconcileOpenShiftRouteControllerManagerConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, minTLSVersion string, cipherSuites []string, networkConfig *configv1.NetworkSpec) error {
	ownerRef.ApplyTo(cm)

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	if configStr, exists := cm.Data[configKey]; exists && len(configStr) > 0 {
		err := util.DeserializeResource(configStr, config, api.Scheme)
		if err != nil {
			return fmt.Errorf("unable to decode existing openshift route controller manager configuration: %w", err)
		}
	}
	if err := reconcileConfig(config, minTLSVersion, cipherSuites, networkConfig); err != nil {
		return err
	}
	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift route controller manager configuration: %w", err)
	}
	cm.Data[configKey] = configStr
	return nil
}

func reconcileConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, minTLSVersion string, cipherSuites []string, networkConfig *configv1.NetworkSpec) error {
	cpath := func(volume, file string) string {
		dir := volumeMounts.Path(routeOCMContainerMain().Name, volume)
		return path.Join(dir, file)
	}
	cfg.TypeMeta = metav1.TypeMeta{
		Kind:       "OpenShiftControllerManagerConfig",
		APIVersion: openshiftcpv1.GroupVersion.String(),
	}

	// network config
	if networkConfig != nil && networkConfig.ExternalIP != nil && len(networkConfig.ExternalIP.AutoAssignCIDRs) > 0 {
		cfg.Ingress.IngressIPNetworkCIDR = networkConfig.ExternalIP.AutoAssignCIDRs[0]
	} else {
		cfg.Ingress.IngressIPNetworkCIDR = ""
	}

	cfg.LeaderElection.Name = "openshift-route-controllers"
	cfg.ServingInfo = &configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			BindAddress: fmt.Sprintf("0.0.0.0:%d", servingPort),
			CertInfo: configv1.CertInfo{
				CertFile: cpath(routeOCMVolumeServingCert().Name, corev1.TLSCertKey),
				KeyFile:  cpath(routeOCMVolumeServingCert().Name, corev1.TLSPrivateKeyKey),
			},
			ClientCA:      cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey),
			MinTLSVersion: minTLSVersion,
			CipherSuites:  cipherSuites,
		},
	}
	return nil
}
