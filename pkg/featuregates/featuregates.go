package featuregates

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/featuregate"
)

// Feature represents a named feature that corresponds to a feature gate
type Feature struct {
	// name is the name of the feature and the name of the feature gate it corresponds to
	name featuregate.Feature
	// enabledFeatureSets is the feature sets in which this feature should be enabled by default
	enabledFeatureSets sets.Set[configv1.FeatureSet]
}

// FeatureOption is a function used to configure a Feature
type FeatureOption func(*Feature)

// WithEnableForFeatureSets returns a FeatureOption to enable the Feature in
// the provided feature sets by default.
func WithEnableForFeatureSets(featureSets ...configv1.FeatureSet) FeatureOption {
	return func(f *Feature) {
		f.enabledFeatureSets = sets.New(featureSets...)
	}
}

// NewFeature is a utility function for easily creating and configuring new Features.
func NewFeature(name featuregate.Feature, opts ...FeatureOption) *Feature {
	feature := &Feature{
		name: name,
	}

	for _, opt := range opts {
		opt(feature)
	}

	return feature
}

// features is a utility struct to represent a collection of enabled/disabled features
type features struct {
	Enabled  sets.Set[featuregate.Feature]
	Disabled sets.Set[featuregate.Feature]
}

// newFeatures is a utility function for creating a new collection of enabled/disabled features
func newFeatures() *features {
	return &features{
		Enabled:  sets.New[featuregate.Feature](),
		Disabled: sets.New[featuregate.Feature](),
	}
}

// FeatureSetAwareFeatures is a collection of enabled/disabled features for a named feature set.
// Each feature set will contain the same features, but may have different states for each feature.
type FeatureSetAwareFeatures map[configv1.FeatureSet]*features

// NewFeatureSetAwareFeatures builds a new FeatureSetAwareFeatures object, setting
// the known feature sets to the fixed feature sets that exist within OpenShift.
// The fixed feature sets within OpenShift are DevPreviewNoUpgrade, TechPreviewNoUpgrade,
// and "" (Default).
func NewFeatureSetAwareFeatures() FeatureSetAwareFeatures {
	out := make(FeatureSetAwareFeatures)
	for _, featureSet := range configv1.AllFixedFeatureSets {
		out[featureSet] = newFeatures()
	}

	return out
}

// AddFeature adds a new feature to the FeatureSetAwareFeatures.
// The new feature is added to all known feature sets.
// Unless explicitly marked as enabled for the known feature set,
// the feature will be added to set of features that is automatically
// disabled for the feature set.
func (fsaf FeatureSetAwareFeatures) AddFeature(feature *Feature) {
	for featureSet, features := range fsaf {
		if feature.enabledFeatureSets.Has(featureSet) {
			features.Enabled.Insert(feature.name)
			continue
		}
		features.Disabled.Insert(feature.name)
	}
}

// FeatureGatesForFeatureSet returns the featuregate.MutableFeatureGate corresponding to the provided featureSet.
// If the provided featureSet is unknown, an error will be returned.
// If the provided featureSet is known, the featuregate.MutableFeatureGate will be returned where
// all features marked as enabled for the featureSet will be enabled by default and all other features
// will be disabled by default.
// If any errors are encountered during the process of building the feature gates for the feature set, an error will be returned.
func (fsaf FeatureSetAwareFeatures) FeatureGatesForFeatureSet(featureSet configv1.FeatureSet) (featuregate.MutableFeatureGate, error) {
	features, ok := fsaf[featureSet]
	if !ok {
		return nil, fmt.Errorf("unknown featureset %q provided", featureSet)
	}

	featureGates := map[featuregate.Feature]featuregate.FeatureSpec{}
	for _, feature := range features.Enabled.UnsortedList() {
		featureGates[featuregate.Feature(feature)] = featuregate.FeatureSpec{Default: true}
	}

	for _, feature := range features.Disabled.UnsortedList() {
		featureGates[featuregate.Feature(feature)] = featuregate.FeatureSpec{Default: false}
	}

	fgs := featuregate.NewFeatureGate()
	err := fgs.Add(featureGates)
	return fgs, err
}
