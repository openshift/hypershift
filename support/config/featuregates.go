package config

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"
)

func FeatureGates(fg configv1.FeatureGateSelection) []string {
	result := []string{}
	var enabled, disabled []configv1.FeatureGateName
	if fg.FeatureSet == configv1.CustomNoUpgrade {
		enabled = fg.CustomNoUpgrade.Enabled
		disabled = fg.CustomNoUpgrade.Disabled
	} else {
		fs, err := features.FeatureSets(features.Hypershift, fg.FeatureSet)
		if err != nil {
			return nil
		}
		for _, fgDescription := range fs.Enabled {
			enabled = append(enabled, fgDescription.FeatureGateAttributes.Name)
		}
		for _, fgDescription := range fs.Disabled {
			disabled = append(disabled, fgDescription.FeatureGateAttributes.Name)
		}
	}
	for _, e := range enabled {
		result = append(result, fmt.Sprintf("%s=true", e))
	}
	for _, d := range disabled {
		result = append(result, fmt.Sprintf("%s=false", d))
	}
	return result
}

// FeatureGatesFromDetails returns a list of feature gates from a FeatureGateDetails object
// in a format to be used by KCM and other components flags.
func FeatureGatesFromDetails(fg *configv1.FeatureGateDetails) []string {
	result := []string{}
	for _, e := range fg.Enabled {
		result = append(result, fmt.Sprintf("%s=true", e.Name))
	}
	for _, d := range fg.Disabled {
		result = append(result, fmt.Sprintf("%s=false", d.Name))
	}
	return result
}

// FeatureGateDetailsFromConfigMap returns the feature gate details from the hostedcluster-gates ConfigMap.
// This can be consumed by FeatureGatesFromDetails to create a []string of feature gates that components can use to set their feature gates flags.
func FeatureGateDetailsFromConfigMap(c client.Reader, ctx context.Context, namespace string, releaseVersion string) (*configv1.FeatureGateDetails, error) {
	hostedClusterGates := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "hostedcluster-gates"}, hostedClusterGates); err != nil {
		return nil, fmt.Errorf("failed to get hostedcluster-gates ConfigMap: %w", err)
	}
	featureGatesData, exists := hostedClusterGates.Data["featureGates"]
	if !exists {
		return nil, fmt.Errorf("failed to get hostedcluster-gates ConfigMap: featureGates key not found")
	}

	var featureGateDetails *configv1.FeatureGateDetails
	if err := yaml.Unmarshal([]byte(featureGatesData), &featureGateDetails); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feature gates configuration: %w", err)
	}
	// Verify that the feature gate details match the targetedrelease version.
	if featureGateDetails.Version != releaseVersion {
		return nil, fmt.Errorf("feature gate version mismatch: expected %s, got %s", releaseVersion, featureGateDetails.Version)
	}
	return featureGateDetails, nil
}
