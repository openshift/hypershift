package ignitionpayload

import (
	supportutil "github.com/openshift/hypershift/support/util"
)

// rolloutHashFormulaVersion is the version of the rollout-hash formula below. It is bumped
// BY HAND in the same change that alters which inputs feed rolloutHash. status.rolloutHashVersion
// records the version used for a CR's current.rolloutHash; when the stored version is older than
// this constant the controller rebaselines the stored hash WITHOUT advancing rolloutGeneration (see
// the reconcile state machine). Mirrors the versioned NodePool config hash (#9007).
const rolloutHashFormulaVersion int64 = 1

// hashInputs carries everything the two hashes need.
type hashInputs struct {
	resolved            ResolvedConfig // validated rollout + management manifests
	releaseVersion      string         // releaseImage.Version()
	pullSecretName      string
	pullSecretContent   []byte
	trustBundleName     string // spec.additionalTrustBundle.Name ("" if unset)
	trustBundleContent  []byte
	rolloutGlobalConfig []byte // bytes of the rolloutGlobalConfig ConfigMap ("" if unset)
	osImageStream       string // spec.osImageStream.Name
	// additionalRolloutInputs is the consumer-authored summary of rollout-relevant inputs that
	// live outside the referenced ConfigMaps (e.g. the API-server endpoint identity carried in the
	// HAProxy management config). It feeds the rollout hash only, never configHash.
	additionalRolloutInputs string
	// hcConfigHash is backwardcompat.GetBackwardCompatibleConfigHash(hcp.Spec.Configuration) — the
	// full cluster-configuration gate value CPO writes into the machine-config-server ConfigMap. It
	// feeds payloadIdentityHash only (a full-config change refreshes the payload for new nodes), not
	// rolloutHash (whose global-config term is the rollout-relevant subset in rolloutGlobalConfig).
	hcConfigHash string
	// cloudConfigHash hashes the platform cloud-provider config ("" for AWS/None). It feeds
	// payloadIdentityHash only, matching the old TokenSecretCloudConfigHashKey (never a rollout
	// trigger).
	cloudConfigHash string
}

// payloadIdentityHash covers EVERYTHING embedded in the rendered payload, including the
// pull-secret and trust-bundle CONTENTS. It is recorded alongside the store entry and makes
// generation idempotent: an in-place credential/CA rotation changes this hash, so the generator
// never serves stale bytes. It deliberately EXCLUDES additionalRolloutInputs, which is a rollout
// trigger and not part of the payload's byte identity.
func payloadIdentityHash(in hashInputs) string {
	return supportutil.HashSimple(in.resolved.RolloutManifests +
		in.resolved.ManagementManifests +
		in.releaseVersion +
		string(in.pullSecretContent) +
		string(in.trustBundleContent) +
		string(in.rolloutGlobalConfig) +
		in.osImageStream +
		in.hcConfigHash +
		in.cloudConfigHash)
}

// rolloutHash covers only rollout-relevant inputs. It EXCLUDES the management (HAProxy) manifests
// and uses the pull-secret/trust-bundle NAMES (not their contents), so a management-side bump or an
// in-place credential rotation does not advance it. It folds in additionalRolloutInputs so a
// consumer can roll running nodes for a change the referenced ConfigMaps would otherwise miss. A
// change here triggers a node rollout.
func rolloutHash(in hashInputs) string {
	return supportutil.HashSimple(in.resolved.RolloutManifests +
		in.releaseVersion +
		in.pullSecretName +
		in.trustBundleName +
		string(in.rolloutGlobalConfig) +
		in.osImageStream +
		in.additionalRolloutInputs)
}
