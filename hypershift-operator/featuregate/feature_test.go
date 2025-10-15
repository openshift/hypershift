package featuregate_test

import (
	"testing"

	"github.com/openshift/hypershift/hypershift-operator/featuregate"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/stretchr/testify/assert"
)

func TestGCPPlatformFeatureGate(t *testing.T) {
	testcases := []struct {
		name                string
		featureSet          configv1.FeatureSet
		expectedGCPPlatform bool
	}{
		{
			name:                "Default feature set should disable GCPPlatform",
			featureSet:          configv1.Default,
			expectedGCPPlatform: false,
		},
		{
			name:                "TechPreviewNoUpgrade feature set should enable GCPPlatform",
			featureSet:          configv1.TechPreviewNoUpgrade,
			expectedGCPPlatform: true,
		},
		{
			name:                "DevPreviewNoUpgrade feature set should disable GCPPlatform",
			featureSet:          configv1.DevPreviewNoUpgrade,
			expectedGCPPlatform: false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			// Configure the feature set
			featuregate.ConfigureFeatureSet(string(tc.featureSet))

			// Check if GCPPlatform feature gate is enabled/disabled as expected
			actualGCPPlatform := featuregate.Gate().Enabled(featuregate.GCPPlatform)
			assert.Equal(t, tc.expectedGCPPlatform, actualGCPPlatform,
				"GCPPlatform feature gate enabled state should match expected value for feature set %s", tc.featureSet)

			// Verify the feature set is correctly configured
			assert.Equal(t, tc.featureSet, featuregate.FeatureSet(),
				"Feature set should be correctly configured")
		})
	}
}

func TestAllHypershiftOperatorFeatureGates(t *testing.T) {
	testcases := []struct {
		name       string
		featureSet configv1.FeatureSet
		expected   map[string]bool
	}{
		{
			name:       "Default feature set",
			featureSet: configv1.Default,
			expected: map[string]bool{
				"AROHCPManagedIdentities": false,
				"OpenStack":               false,
				"GCPPlatform":             false,
			},
		},
		{
			name:       "TechPreviewNoUpgrade feature set",
			featureSet: configv1.TechPreviewNoUpgrade,
			expected: map[string]bool{
				"AROHCPManagedIdentities": true,
				"OpenStack":               true,
				"GCPPlatform":             true,
			},
		},
		{
			name:       "DevPreviewNoUpgrade feature set",
			featureSet: configv1.DevPreviewNoUpgrade,
			expected: map[string]bool{
				"AROHCPManagedIdentities": false,
				"OpenStack":               false,
				"GCPPlatform":             false,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			// Configure the feature set
			featuregate.ConfigureFeatureSet(string(tc.featureSet))

			// Test AROHCPManagedIdentities
			actualAROHCP := featuregate.Gate().Enabled(featuregate.AROHCPManagedIdentities)
			assert.Equal(t, tc.expected["AROHCPManagedIdentities"], actualAROHCP,
				"AROHCPManagedIdentities should be %v for feature set %s",
				tc.expected["AROHCPManagedIdentities"], tc.featureSet)

			// Test OpenStack
			actualOpenStack := featuregate.Gate().Enabled(featuregate.OpenStack)
			assert.Equal(t, tc.expected["OpenStack"], actualOpenStack,
				"OpenStack should be %v for feature set %s",
				tc.expected["OpenStack"], tc.featureSet)

			// Test GCPPlatform
			actualGCPPlatform := featuregate.Gate().Enabled(featuregate.GCPPlatform)
			assert.Equal(t, tc.expected["GCPPlatform"], actualGCPPlatform,
				"GCPPlatform should be %v for feature set %s",
				tc.expected["GCPPlatform"], tc.featureSet)
		})
	}
}

func TestFeatureGateConstants(t *testing.T) {
	// Test that our feature gate constants are properly defined
	assert.Equal(t, "AROHCPManagedIdentities", string(featuregate.AROHCPManagedIdentities))
	assert.Equal(t, "OpenStack", string(featuregate.OpenStack))
	assert.Equal(t, "GCPPlatform", string(featuregate.GCPPlatform))
}
