package config

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"

	"github.com/blang/semver"
)

// BuildGenericControllerConfigData builds a GenericControllerConfig YAML string
// with the specified bind address, bind network, and TLS security profile.
// This is used by various control plane operators to configure their serving info.
func BuildGenericControllerConfigData(ctx context.Context, bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile, featureGates []string, payloadVersion semver.Version) (string, error) {
	cipherSuites, err := CipherSuites(profile)
	if err != nil {
		return "", fmt.Errorf("failed to get cipher suites: %w", err)
	}
	minTLSVersion, err := MinTLSVersion(profile)
	if err != nil {
		return "", fmt.Errorf("failed to get min TLS version: %w", err)
	}

	// We only need to worry about TLS Groups from the version we started
	// to support them (v5.1.0) onwards.
	supportedOnMajors := payloadVersion.Major > 5
	supportedOnMinors := payloadVersion.Major == 5 && payloadVersion.Minor >= 1
	supportsTLSGroups := supportedOnMinors || supportedOnMajors

	var curvePreferences []int32
	featureGateParam := fmt.Sprintf("%s=true", features.FeatureGateTLSGroupPreferences)
	if supportsTLSGroups && slices.Contains(featureGates, featureGateParam) {
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
func SetGenericControllerConfig(ctx context.Context, bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile, cm *corev1.ConfigMap, featureGates []string, payloadVersion semver.Version) error {
	data, err := BuildGenericControllerConfigData(
		ctx,
		bindAddress,
		bindNetwork,
		profile,
		featureGates,
		payloadVersion,
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
