package featuregates_test

import (
	"testing"

	"github.com/openshift/hypershift/pkg/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatingFeatureGates(t *testing.T) {
	type testcase struct {
		name     string
		features []*featuregates.Feature
		expected map[configv1.FeatureSet]map[featuregate.Feature]bool
	}

	testcases := []testcase{
		{
			name: "configuring a feature gate with no featureset, should never be enabled",
			features: []*featuregates.Feature{
				featuregates.NewFeature("Foo"),
			},
			expected: map[configv1.FeatureSet]map[featuregate.Feature]bool{
				configv1.Default: {
					"Foo": false,
				},
				configv1.TechPreviewNoUpgrade: {
					"Foo": false,
				},
				configv1.DevPreviewNoUpgrade: {
					"Foo": false,
				},
			},
		},
		{
			name: "configuring featuregates with specific featureset enablement, should only be enabled in featuresets it is explicitly enabled in",
			features: []*featuregates.Feature{
				featuregates.NewFeature("Foo", featuregates.WithEnableForFeatureSets(configv1.AllFixedFeatureSets...)),
				featuregates.NewFeature("Bar", featuregates.WithEnableForFeatureSets(configv1.Default)), // enabling only in default should generally never be done, but theoretically possible
				featuregates.NewFeature("Baz", featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade)),
				featuregates.NewFeature("Qux", featuregates.WithEnableForFeatureSets(configv1.DevPreviewNoUpgrade, configv1.TechPreviewNoUpgrade)),
				featuregates.NewFeature("Corge", featuregates.WithEnableForFeatureSets(configv1.DevPreviewNoUpgrade)),
			},
			expected: map[configv1.FeatureSet]map[featuregate.Feature]bool{
				configv1.Default: {
					"Foo":   true,
					"Bar":   true,
					"Baz":   false,
					"Qux":   false,
					"Corge": false,
				},
				configv1.TechPreviewNoUpgrade: {
					"Foo":   true,
					"Bar":   false,
					"Baz":   true,
					"Qux":   true,
					"Corge": false,
				},
				configv1.DevPreviewNoUpgrade: {
					"Foo":   true,
					"Bar":   false,
					"Baz":   false,
					"Qux":   true,
					"Corge": true,
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			features := featuregates.NewFeatureSetAwareFeatures()
			for _, feature := range tc.features {
				features.AddFeature(feature)
			}

			for fs, expectedFeatureState := range tc.expected {
				fg, err := features.FeatureGatesForFeatureSet(fs)
				require.NoError(t, err, "should not receive an error building feature gates for a featureset when testing with only known featureset names")

				for feat, expectedEnabledState := range expectedFeatureState {
					assert.Equal(t, fg.Enabled(feat), expectedEnabledState, "actual featuregate enabled state does not match expected", "featureset", fs, "featuregate", feat)
				}
			}
		})
	}
}

func TestConfiguringUnknownFeatureSetErrors(t *testing.T) {
	features := featuregates.NewFeatureSetAwareFeatures()
	_, err := features.FeatureGatesForFeatureSet("FooBar")
	assert.Error(t, err, "configuring an unknown featureset should result in an error")
}
