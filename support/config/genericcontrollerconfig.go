package config

import (
	"context"
	"encoding/json"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"
)

// BuildGenericControllerConfigData builds a GenericControllerConfig YAML string
// with the specified bind address, bind network, and TLS security profile.
// This is used by various control plane operators to configure their serving info.
func BuildGenericControllerConfigData(ctx context.Context, bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile, applyTLSGroups bool) (string, error) {
	cipherSuites, err := CipherSuites(profile)
	if err != nil {
		return "", fmt.Errorf("failed to get cipher suites: %w", err)
	}
	minTLSVersion, err := MinTLSVersion(profile)
	if err != nil {
		return "", fmt.Errorf("failed to get min TLS version: %w", err)
	}

	var curvePreferences []int32
	if applyTLSGroups {
		curves, err := CurvePreferences(ctx, profile)
		if err != nil {
			return "", fmt.Errorf("failed to read tls groups: %w", err)
		}
		for _, curve := range curves {
			curvePreferences = append(curvePreferences, int32(curve))
		}
	}

	controllerConfig := configv1.GenericControllerConfig{
		ServingInfo: configv1.HTTPServingInfo{
			ServingInfo: configv1.ServingInfo{
				BindAddress:      bindAddress,
				BindNetwork:      bindNetwork,
				CipherSuites:     cipherSuites,
				MinTLSVersion:    minTLSVersion,
				CurvePreferences: curvePreferences,
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
func SetGenericControllerConfig(ctx context.Context, bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile, cm *corev1.ConfigMap, applyTLSGroups bool) error {
	data, err := BuildGenericControllerConfigData(
		ctx,
		bindAddress,
		bindNetwork,
		profile,
		applyTLSGroups,
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
