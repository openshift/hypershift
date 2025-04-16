package featuregate

import (
	"github.com/openshift/hypershift/pkg/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	// AROHCPManagedIdentities is a feature gate for enabling HCP components to authenticate with Azure by client certificate
	// owner: @username
	// alpha: v0.1.49
	// beta x.y.z
	AROHCPManagedIdentities featuregate.Feature = "AROHCPManagedIdentities"

	// OpenStack is a feature gate for running clusters on OpenStack.
	// owner: @username
	// alpha: v0.1.49
	// beta: x.y.z
	OpenStack featuregate.Feature = "OpenStack"
)

// Initialize new features here
var (
	allFeatures = featuregates.NewFeatureSetAwareFeatures()

	aroHCPManagedIdentitiesFeature = featuregates.NewFeature(AROHCPManagedIdentities, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	openStackFeature               = featuregates.NewFeature(OpenStack, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(aroHCPManagedIdentitiesFeature)
	allFeatures.AddFeature(openStackFeature)

	// Default to configuring the Default featureset
	ConfigureFeatureSet(string(configv1.Default))
}

// globalGate is a global variable meant to store the featuregate.MutableFeatureGate
// for the currently configured featureset. This makes it easier to check
// which feature gates are currently enabled/disabled in other places.
var globalGate featuregate.MutableFeatureGate

// Gate returns the currently configured featuregates. Useful
// for determining what feature gates are enabled or configuring the feature gates.
// Example: Gate().Enabled("Foo")
func Gate() featuregate.MutableFeatureGate {
	return globalGate
}

// globalFeatureSet is a global variable meant to store the
// currently configured feature set. This is useful when
// needing to perform specific actions, like configuring
// the control plane operator, based on the currently configured
// feature set.
var globalFeatureSet configv1.FeatureSet

// FeatureSet returns the currently configured feature set.
func FeatureSet() configv1.FeatureSet {
	return globalFeatureSet
}

// ConfigureFeatureSet is used to configure the feature gates and feature set based on the provided featureSet.
// The provided featureSet must be a known feature set name.
// ConfigureFeatureSet should only be called once on startup.
// ConfigureFeatureSet is not thread-safe.
func ConfigureFeatureSet(featureSet string) {
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.FeatureSet(featureSet))
	if err != nil {
		// If we encounter an error due to an unknown featureset, just assume the default
		return
	}

	globalFeatureSet = configv1.FeatureSet(featureSet)
	globalGate = featureGate
}
