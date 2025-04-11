package featuregates

import (
	"github.com/openshift/hypershift/pkg/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	ExternalOIDCWithUIDAndExtraClaimMappings featuregate.Feature = "ExternalOIDCWithUIDAndExtraClaimMappings"
)

// Initialize new features here
var (
	allFeatures = featuregates.NewFeatureSetAwareFeatures()

	externalOIDCWithUIDAndExtraClaimMappingsFeature = featuregates.NewFeature(ExternalOIDCWithUIDAndExtraClaimMappings, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(externalOIDCWithUIDAndExtraClaimMappingsFeature)

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
