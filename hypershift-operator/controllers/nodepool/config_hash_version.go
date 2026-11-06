package nodepool

import "strings"

// The v2 rollout baseline is stored separately from the legacy name-based
// rollout annotations. Upstream's seedRolloutAnnotation and token state machine
// adopt existing bootstrap identities once, including in-flight rollouts, before
// tracking subsequent CA content changes. Never rewrite a completed payload hash
// merely to migrate a rollout formula: it identifies the secrets still in use.
const (
	// ConfigHashVersionV1 is the legacy name-based payload hash formula.
	ConfigHashVersionV1 = "v1"
	// ConfigHashVersionV2 includes trust-bundle content in payload and rollout hashes.
	ConfigHashVersionV2 = "v2"
	// CurrentConfigHashVersion identifies the active trust-bundle hash formula.
	CurrentConfigHashVersion            = ConfigHashVersionV2
	nodePoolAnnotationConfigHashVersion = "hypershift.openshift.io/nodePoolConfigHashVersion"
)

// ConfigHashVersion returns the hash formula supported by the selected CPO.
func (cg *ConfigGenerator) ConfigHashVersion() string {
	if cg.useLegacyTrustBundleHash {
		return ConfigHashVersionV1
	}
	return CurrentConfigHashVersion
}

func (cg *ConfigGenerator) rolloutAnnotationKey(key string) string {
	if cg.useLegacyTrustBundleHash {
		return strings.TrimSuffix(key, "V2")
	}
	return key
}

// RolloutConfigAnnotation returns the completed baseline key for this formula.
// Keeping the v1 and v2 baselines separate lets a later CPO upgrade seed v2
// without replacing nodes just because content hashing became available.
func (cg *ConfigGenerator) RolloutConfigAnnotation() string {
	return cg.rolloutAnnotationKey(nodePoolAnnotationCurrentRolloutConfig)
}
