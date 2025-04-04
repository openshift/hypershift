package featuregates

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/featuregate"
)

// TODO: Write helpful GoDoc.

// TODO: Took inspiration from the openshift/api feature definition process.
// openshift/api includes requiring things like linking to enhancement proposals,
// setting an associated JIRA component, and a "responsible person".
// Might be worth considering further what additional information we want to
// include in the Hypershift component feature definitions, but for now
// just the feature name and the feature sets it is automatically enabled in
// should be sufficient
type Feature struct {
	name               featuregate.Feature
	enabledFeatureSets sets.Set[configv1.FeatureSet]
}

type FeatureOption func(*Feature)

func WithEnableForFeatureSets(featureSets ...configv1.FeatureSet) FeatureOption {
	return func(f *Feature) {
		f.enabledFeatureSets = sets.New(featureSets...)
	}
}

func NewFeature(name featuregate.Feature, opts ...FeatureOption) *Feature {
	feature := &Feature{
		name: name,
	}

	for _, opt := range opts {
		opt(feature)
	}

	return feature
}

type Features struct {
	Enabled  sets.Set[featuregate.Feature]
	Disabled sets.Set[featuregate.Feature]
}

func NewFeatures() *Features {
	return &Features{
		Enabled:  sets.New[featuregate.Feature](),
		Disabled: sets.New[featuregate.Feature](),
	}
}

type FeatureSetAwareFeatures map[configv1.FeatureSet]*Features

func NewFeatureSetAwareFeatures() FeatureSetAwareFeatures {
	out := make(FeatureSetAwareFeatures)
	for _, featureSet := range configv1.AllFixedFeatureSets {
		out[featureSet] = NewFeatures()
	}

	return out
}

func (fsaf FeatureSetAwareFeatures) AddFeature(feature *Feature) {
	for featureSet, features := range fsaf {
		if feature.enabledFeatureSets.Has(featureSet) {
			features.Enabled.Insert(feature.name)
			continue
		}
		features.Disabled.Insert(feature.name)
	}
}

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
