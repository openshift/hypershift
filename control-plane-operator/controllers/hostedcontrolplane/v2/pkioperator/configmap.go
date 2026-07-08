package pkioperator

import (
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

// adaptControllerConfig uses the HostedControlPlane to derive the PKI
// operator controller configuration. We mostly worry about making sure
// that the operator is using the correct TLS profile settings.
func adaptControllerConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	data, err := config.BuildGenericControllerConfigData(
		"0.0.0.0:8443",
		"tcp4",
		cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile(),
	)
	if err != nil {
		return err
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	cm.Data["config.yaml"] = data
	return nil
}
