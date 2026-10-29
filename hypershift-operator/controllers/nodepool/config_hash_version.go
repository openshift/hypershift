package nodepool

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
