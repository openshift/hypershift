package storage

import (
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

func adaptControllerConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	data, err := config.BuildGenericControllerConfigData(
		"0.0.0.0:8443",
		"",
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
