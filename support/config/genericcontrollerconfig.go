package config

import (
	"encoding/json"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"
)

// BuildGenericControllerConfigData builds a GenericControllerConfig YAML string
// with the specified bind address, bind network, and TLS security profile.
// This is used by various control plane operators to configure their serving info.
func BuildGenericControllerConfigData(bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile) (string, error) {
	controllerConfig := configv1.GenericControllerConfig{
		ServingInfo: configv1.HTTPServingInfo{
			ServingInfo: configv1.ServingInfo{
				BindAddress:   bindAddress,
				BindNetwork:   bindNetwork,
				CipherSuites:  CipherSuites(profile),
				MinTLSVersion: MinTLSVersion(profile),
			},
		},
	}

	asJSON, err := json.Marshal(controllerConfig)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal config: %w", err)
	}

	asMap := map[string]any{}
	if err := json.Unmarshal(asJSON, &asMap); err != nil {
		return "", fmt.Errorf("failed to json unmarshal config: %w", err)
	}

	asMap["apiVersion"] = configv1.GroupVersion.String()
	asMap["kind"] = "GenericControllerConfig"

	data, err := yaml.Marshal(asMap)
	if err != nil {
		return "", fmt.Errorf("failed to yaml marshal config: %w", err)
	}

	return string(data), nil
}

// SetGenericControllerConfig builds a GenericControllerConfig YAML and sets it
// in the ConfigMap's config.yaml data field. This is a helper function used by
// NewGenericControllerConfigAdapter in support/controlplane-component.
func SetGenericControllerConfig(bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile, cm *corev1.ConfigMap) error {
	data, err := BuildGenericControllerConfigData(
		bindAddress,
		bindNetwork,
		profile,
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
