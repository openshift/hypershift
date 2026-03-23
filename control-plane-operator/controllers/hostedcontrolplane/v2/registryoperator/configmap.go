package registryoperator

import (
	"encoding/json"
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"
)

// adaptControllerConfig uses the HostedControlPlane to derive the image
// registry controller configuration. We mostly worry about making sure
// that the operator is using the correct TLS profile settings.
func adaptControllerConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	profile := cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile()
	controllerConfig := configv1.GenericControllerConfig{
		ServingInfo: configv1.HTTPServingInfo{
			ServingInfo: configv1.ServingInfo{
				BindAddress:   ":60000",
				CipherSuites:  config.CipherSuites(profile),
				MinTLSVersion: config.MinTLSVersion(profile),
			},
		},
	}

	asJSON, err := json.Marshal(controllerConfig)
	if err != nil {
		return fmt.Errorf("failed to json marshal config: %w", err)
	}

	asMap := map[string]any{}
	if err := json.Unmarshal(asJSON, &asMap); err != nil {
		return fmt.Errorf("failed to json unmarshal config: %w", err)
	}

	asMap["apiVersion"] = configv1.GroupVersion.String()
	asMap["kind"] = "GenericControllerConfig"

	data, err := yaml.Marshal(asMap)
	if err != nil {
		return fmt.Errorf("failed to yaml marshal config: %w", err)
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	cm.Data["config.yaml"] = string(data)
	return nil
}
