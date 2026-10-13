package routecm

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	configKey = "config.yaml"
)

func adaptConfigMap(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	if configStr, exists := cm.Data[configKey]; !exists || len(configStr) == 0 {
		return fmt.Errorf("expected an existing openshift route controller manager configuration")
	}

	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	if err := util.DeserializeResource(cm.Data[configKey], config, api.Scheme); err != nil {
		return fmt.Errorf("unable to decode existing openshift route controller manager configuration: %w", err)
	}

	adaptConfig(config, cpContext.HCP.Spec.Configuration, cpContext.HCP.Spec.Capabilities)
	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize openshift route controller manager configuration: %w", err)
	}

	cm.Data[configKey] = configStr
	return nil
}

func adaptConfig(cfg *openshiftcpv1.OpenShiftControllerManagerConfig, configuration *hyperv1.ClusterConfiguration, caps *hyperv1.Capabilities) {
	// network config
	if cidrs := configuration.GetAutoAssignCIDRs(); len(cidrs) > 0 {
		cfg.Ingress.IngressIPNetworkCIDR = cidrs[0]
	}

	cfg.ServingInfo.MinTLSVersion = config.MinTLSVersion(configuration.GetTLSSecurityProfile())
	cfg.ServingInfo.CipherSuites = config.CipherSuites(configuration.GetTLSSecurityProfile())
}
