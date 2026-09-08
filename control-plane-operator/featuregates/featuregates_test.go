package featuregates

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
)

func TestEnabledForFeatureSet(t *testing.T) {
	testCases := []struct {
		name       string
		feature    featuregate.Feature
		featureSet configv1.FeatureSet
		enabled    bool
	}{
		{"When external claims sourcing is queried in TechPreview it should be enabled", ExternalOIDCExternalClaimsSourcing, configv1.TechPreviewNoUpgrade, true},
		{"When external claims sourcing is queried in Default it should be disabled", ExternalOIDCExternalClaimsSourcing, configv1.Default, false},
		{"When UID and extra mappings are queried in Default it should be enabled", ExternalOIDCWithUIDAndExtraClaimMappings, configv1.Default, true},
		{"When the feature is unknown it should be disabled", "Unknown", configv1.TechPreviewNoUpgrade, false},
		{"When the feature set is unknown it should be disabled", ExternalOIDCExternalClaimsSourcing, "Unknown", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			gate := Gate()
			globalEnabled := gate.Enabled(ExternalOIDCExternalClaimsSourcing)
			g.Expect(EnabledForFeatureSet(tc.feature, tc.featureSet)).To(Equal(tc.enabled))
			g.Expect(Gate()).To(BeIdenticalTo(gate))
			g.Expect(Gate().Enabled(ExternalOIDCExternalClaimsSourcing)).To(Equal(globalEnabled))
		})
	}
}
