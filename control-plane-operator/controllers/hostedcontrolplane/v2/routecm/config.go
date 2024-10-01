package routecm

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	configKey = "config.yaml"
)

func adaptConfigMap(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	hcp := cpContext.HCP
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	if configStr, exists := cm.Data[configKey]; !exists || len(configStr) == 0 {
		return fmt.Errorf("expected an existing openshift route controller manager configuration")
	}

	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	if err := util.DeserializeResource(cm.Data[configKey], config, api.Scheme); err != nil {
		return fmt.Errorf("unable to decode existing openshift route controller manager configuration: %w", err)
	}

	var networkConfig *configv1.NetworkSpec
	if hcp.Spec.Configuration != nil {
		networkConfig = hcp.Spec.Configuration.Network
	}
	if err := reconcileConfig(config, minTLSVersion(hcp), cipherSuites(hcp), networkConfig); err != nil {
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
	// network config
	if networkConfig != nil && networkConfig.ExternalIP != nil && len(networkConfig.ExternalIP.AutoAssignCIDRs) > 0 {
		cfg.Ingress.IngressIPNetworkCIDR = networkConfig.ExternalIP.AutoAssignCIDRs[0]
	} else {
		cfg.Ingress.IngressIPNetworkCIDR = ""
	}

	cfg.ServingInfo.CertFile = path.Join("/etc/kubernetes/certs", corev1.TLSCertKey)
	cfg.ServingInfo.KeyFile = path.Join("/etc/kubernetes/certs", corev1.TLSPrivateKeyKey)
	cfg.ServingInfo.ClientCA = path.Join("/etc/kubernetes/client-ca", certs.CASignerCertMapKey)

	cfg.ServingInfo.MinTLSVersion = minTLSVersion
	cfg.ServingInfo.CipherSuites = cipherSuites

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
