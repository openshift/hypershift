package featuregates

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/pkg/featuregates"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	Foo featuregate.Feature = "Foo"
	Bar featuregate.Feature = "Bar"
)

var (
	allFeatures = featuregates.NewFeatureSetAwareFeatures()

	fooFeature = featuregates.NewFeature(Foo, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade, configv1.Default))
	barFeature = featuregates.NewFeature(Bar, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(fooFeature)
	allFeatures.AddFeature(barFeature)

	// Default to configuring the Default featureset
	runtime.Must(ConfigureFeatureSet(string(configv1.Default)))
	globalFeatureSet = configv1.Default
}

var globalGate featuregate.FeatureGate

func Gate() featuregate.FeatureGate {
	return globalGate
}

var globalFeatureSet configv1.FeatureSet

func FeatureSet() configv1.FeatureSet {
	return globalFeatureSet
}

func ConfigureFeatureSet(featureSet string) error {
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.FeatureSet(featureSet))
	if err != nil {
		return err
	}

	globalFeatureSet = configv1.FeatureSet(featureSet)

	globalGate = featureGate
	return nil
}
