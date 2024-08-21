package routecm

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	configKey = "config.yaml"
)

func (r *RouteControllerManagerReconciler) reconcileConfigMap(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	hcp := cpContext.HCP
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
	var networkConfig *configv1.NetworkSpec
	if hcp.Spec.Configuration != nil {
		networkConfig = hcp.Spec.Configuration.Network
	}
	if err := reconcileConfig(config, minTLSVersion(hcp), cipherSuites(hcp), networkConfig, r.Volumes(cpContext)); err != nil {
		return err
	}
	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift route controller manager configuration: %w", err)
	}
	cm.Data[configKey] = configStr
	return nil
}

func reconcileConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, minTLSVersion string, cipherSuites []string, networkConfig *configv1.NetworkSpec, volumes component.Volumes) error {
	cpath := func(volume, file string) string {
		dir := volumes.Path(routeOCMContainerMain().Name, volume)
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
				CertFile: cpath(servingCertVolumeName, corev1.TLSCertKey),
				KeyFile:  cpath(servingCertVolumeName, corev1.TLSPrivateKeyKey),
			},
			ClientCA:      cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey),
			MinTLSVersion: minTLSVersion,
			CipherSuites:  cipherSuites,
		},
	}
	return nil
}

func minTLSVersion(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.APIServer != nil {
		return config.MinTLSVersion(hcp.Spec.Configuration.APIServer.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func cipherSuites(hcp *hyperv1.HostedControlPlane) []string {
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.APIServer != nil {
		return config.CipherSuites(hcp.Spec.Configuration.APIServer.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}
