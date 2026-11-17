package featuregates

import (
	"github.com/openshift/hypershift/pkg/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	ExternalOIDCWithUIDAndExtraClaimMappings featuregate.Feature = "ExternalOIDCWithUIDAndExtraClaimMappings"
	ExternalOIDCWithUpstreamParity           featuregate.Feature = "ExternalOIDCWithUpstreamParity"
	KubeletHAProxyRouting                    featuregate.Feature = "KubeletHAProxyRouting"
	ExternalOIDCAsWebhook                    featuregate.Feature = "ExternalOIDCAsWebhook"
	// IgnitionPayloadSystem mirrors the HyperShift Operator gate of the same name. The CPO reads it
	// (propagated via HYPERSHIFT_FEATURESET) only to stand its ignition components down: when the gate
	// is active the HO owns the re-architected ignition system, so the CPO must not also reconcile the
	// same-named ignition-server / ignition-server-proxy components.
	IgnitionPayloadSystem featuregate.Feature = "IgnitionPayloadSystem"
)

// Initialize new features here
var (
	allFeatures = featuregates.NewFeatureSetAwareFeatures()

	externalOIDCWithUIDAndExtraClaimMappingsFeature = featuregates.NewFeature(ExternalOIDCWithUIDAndExtraClaimMappings, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade, configv1.Default))
	externalOIDCWithUpstreamParityFeature           = featuregates.NewFeature(ExternalOIDCWithUpstreamParity, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade, configv1.Default))
	kubeletHAProxyRoutingFeature                    = featuregates.NewFeature(KubeletHAProxyRouting, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	externalOIDCAsWebhook                           = featuregates.NewFeature(ExternalOIDCAsWebhook, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	// ignitionPayloadSystemFeature is intentionally NOT enabled in any feature set yet: the HyperShift
	// Operator side that takes ownership of ignition is not landing with this change, so enabling the gate
	// anywhere would make the CPO stand its ignition components down with nothing serving them. A later
	// change adds it to a feature set once the operator side is merged.
	ignitionPayloadSystemFeature = featuregates.NewFeature(IgnitionPayloadSystem)
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(externalOIDCWithUIDAndExtraClaimMappingsFeature)
	allFeatures.AddFeature(externalOIDCWithUpstreamParityFeature)
	allFeatures.AddFeature(kubeletHAProxyRoutingFeature)
	allFeatures.AddFeature(externalOIDCAsWebhook)
	allFeatures.AddFeature(ignitionPayloadSystemFeature)
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
