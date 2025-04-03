package featuregates

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	Foo featuregate.Feature = "Foo"
	Bar featuregate.Feature = "Bar"
)

var (
	fooFeature = NewFeature(Foo, WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade, configv1.Default))
	barFeature = NewFeature(Bar, WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(fooFeature)
	allFeatures.AddFeature(barFeature)

	// Default to configuring the Default featureset
	runtime.Must(ConfigureFeatureSet(string(configv1.Default)))
}

var gate featuregate.FeatureGate

func Gate() featuregate.FeatureGate {
	return gate
}

func ConfigureFeatureSet(featureSet string) error {
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.FeatureSet(featureSet))
	if err != nil {
		return err
	}

	gate = featureGate
	return nil
}

var allFeatures = NewFeatureSetAwareFeatures()

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

func (fsaf FeatureSetAwareFeatures) FeatureGatesForFeatureSet(featureSet configv1.FeatureSet) (featuregate.FeatureGate, error) {
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
