package featuregates

import (
	"github.com/openshift/hypershift/pkg/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	ExternalOIDCWithUIDAndExtraClaimMappings featuregate.Feature = "ExternalOIDCWithUIDAndExtraClaimMappings"
	AWSServiceLBNetworkSecurityGroup         featuregate.Feature = "AWSServiceLBNetworkSecurityGroup"
)

// Initialize new features here
var (
	allFeatures = featuregates.NewFeatureSetAwareFeatures()

	externalOIDCWithUIDAndExtraClaimMappingsFeature = featuregates.NewFeature(ExternalOIDCWithUIDAndExtraClaimMappings, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade, configv1.Default))
	//awsServiceLBNetworkSecurityGroupFeature         = featuregates.NewFeature(AWSServiceLBNetworkSecurityGroup, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(externalOIDCWithUIDAndExtraClaimMappingsFeature)
	//allFeatures.AddFeature(awsServiceLBNetworkSecurityGroupFeature)

	// Default to configuring the Default featureset
	ConfigureFeatureSet(string(configv1.Default))
}

// globalGate is a global variable meant to store the featuregate.FeatureGate
// for the currently configured featureset. This makes it easier to check
// which feature gates are currently enabled/disabled in other places.
var globalGate featuregate.FeatureGate

// Gate returns the currently configured featuregates. Useful
// for determining what feature gates are enabled.
// Example: Gate().Enabled("Foo")
func Gate() featuregate.FeatureGate {
	return globalGate
}

// ConfigureFeatureSet is used to configure the feature gates based on the provided featureSet.
// The provided featureSet must be a known feature set name.
// ConfigureFeatureSet should only be called once on startup.
// ConfigureFeatureSet is not thread-safe.
func ConfigureFeatureSet(featureSet string) {
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.FeatureSet(featureSet))
	if err != nil {
		// If we encounter an error due to an unknown featureset, just assume the default
		return
	}

	globalGate = featureGate
}

// IsFeatureEnabledInFeatureGateSpec checks if a feature gate is enabled based on a FeatureGateSpec.
// This allows checking feature gates for a given configuration without changing the global gate.
// It handles both fixed feature sets (Default, TechPreviewNoUpgrade, DevPreviewNoUpgrade) and
// custom feature sets (CustomNoUpgrade) where features are explicitly enabled/disabled.
// If the feature gate spec is nil, it falls back to checking the global gate.
/*
func IsFeatureEnabledInFeatureGateSpec(featureGateSpec *configv1.FeatureGateSpec, feature featuregate.Feature) bool {
	if featureGateSpec == nil {
		// No feature gate configuration, fall back to global gate
		return globalGate.Enabled(feature)
	}

	featureSet := featureGateSpec.FeatureSet

	// Handle CustomNoUpgrade feature sets by checking the explicit enabled/disabled lists
	if featureSet == configv1.CustomNoUpgrade {
		if featureGateSpec.CustomNoUpgrade == nil {
			// CustomNoUpgrade without custom configuration, fall back to global gate
			return globalGate.Enabled(feature)
		}

		featureName := configv1.FeatureGateName(feature)

		// Check if explicitly disabled
		for _, disabled := range featureGateSpec.CustomNoUpgrade.Disabled {
			if disabled == featureName {
				return false
			}
		}

		// Check if explicitly enabled
		for _, enabled := range featureGateSpec.CustomNoUpgrade.Enabled {
			if enabled == featureName {
				return true
			}
		}

		// Not in either list, fall back to the feature's default for Default feature set
		featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.Default)
		if err != nil {
			return globalGate.Enabled(feature)
		}
		return featureGate.Enabled(feature)
	}

	// Handle fixed feature sets (Default, TechPreviewNoUpgrade, DevPreviewNoUpgrade)
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(featureSet)
	if err != nil {
		// If there's an error (e.g., unknown feature set), fall back to checking the global gate
		return globalGate.Enabled(feature)
	}

	return featureGate.Enabled(feature)
}
*/
