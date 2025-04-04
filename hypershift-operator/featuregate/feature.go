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
	globalFeatureSet = configv1.Default
}

var globalGate featuregate.MutableFeatureGate

// NOTE: MutableFeatureGate returned for backwards compatibility
func Gate() featuregate.MutableFeatureGate {
	return globalGate
}

var globalFeatureSet configv1.FeatureSet

func FeatureSet() configv1.FeatureSet {
	return globalFeatureSet
}

func ConfigureFeatureSet(featureSet string) {
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.FeatureSet(featureSet))
	if err != nil {
		// If we encounter an error due to an unknown featureset, just assume the default
		return
	}

	globalFeatureSet = configv1.FeatureSet(featureSet)
	globalGate = featureGate
}
