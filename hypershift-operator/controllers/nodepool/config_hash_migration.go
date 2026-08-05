package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"
)

// legacyHashWithoutVersion is the NodePool config hash formula used before trust-bundle
// ConfigMap content was included. It hashed the additionalTrustBundle ConfigMap name
// (not ca-bundle.crt content) and did not include proxy.trustedCA content.
func legacyHashWithoutVersion(mcoRawConfig, pullSecretName, additionalTrustBundleName, rhelStream string) string {
	return supportutil.HashSimple(mcoRawConfig + pullSecretName + additionalTrustBundleName + rhelStream)
}

// legacyHash is the full NodePool hash formula used before trust-bundle content hashing.
func legacyHash(mcoRawConfig, version, pullSecretName, additionalTrustBundleName, globalConfig, rhelStream string) string {
	return supportutil.HashSimple(mcoRawConfig + version + pullSecretName + additionalTrustBundleName + globalConfig + rhelStream)
}

func additionalTrustBundleName(hc *hyperv1.HostedCluster) string {
	if hc != nil && hc.Spec.AdditionalTrustBundle != nil {
		return hc.Spec.AdditionalTrustBundle.Name
	}
	return ""
}

// maybeSeedTrustBundleContentHashBaseline rewrites NodePool current-config annotations from the
// legacy name-based trust-bundle hash formula to the content-based formula when the annotations
// still match the legacy baseline. This one-time seed establishes the content-based hash as the
// current baseline without treating the formula change itself as a config update that should
// roll Nodes. Subsequent real ca-bundle.crt changes still trigger rollouts normally.
//
// Returns true when annotations were updated.
func maybeSeedTrustBundleContentHashBaseline(nodePool *hyperv1.NodePool, cg *ConfigGenerator) bool {
	if nodePool == nil || cg == nil || cg.rolloutConfig == nil || cg.hostedCluster == nil || cg.releaseImage == nil {
		return false
	}

	atbName := additionalTrustBundleName(cg.hostedCluster)
	legacyHWV := legacyHashWithoutVersion(cg.mcoRawConfig, cg.pullSecretName, atbName, cg.rhelStream)
	legacyH := legacyHash(cg.mcoRawConfig, cg.releaseImage.Version(), cg.pullSecretName, atbName, cg.globalConfig, cg.rhelStream)
	newHWV := cg.HashWithoutVersion()
	newH := cg.Hash()

	// No formula delta for this HostedCluster (e.g. no trust bundles configured).
	if legacyHWV == newHWV && legacyH == newH {
		return false
	}

	annotations := nodePool.GetAnnotations()
	if len(annotations) == 0 {
		return false
	}
	current := annotations[nodePoolAnnotationCurrentConfig]
	currentVersion := annotations[nodePoolAnnotationCurrentConfigVersion]

	// Only migrate a stable baseline produced by the legacy formula.
	if current != legacyHWV {
		return false
	}
	if currentVersion != "" && currentVersion != legacyH {
		return false
	}
	if current == newHWV && (currentVersion == "" || currentVersion == newH) {
		return false
	}

	if nodePool.Annotations == nil {
		nodePool.Annotations = make(map[string]string)
	}
	nodePool.Annotations[nodePoolAnnotationCurrentConfig] = newHWV
	nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = newH
	return true
}

// shouldSkipUserDataSecretPropagation reports whether a differing user-data Secret name should
// not be written to the MachineDeployment/MachineSet. After a trust-bundle hash-formula migration
// seeds the NodePool annotations, config and version targets already match the baseline while the
// Secret name still encodes the previous hash. Rewriting DataSecretName in that case would spuriously
// roll Nodes.
func shouldSkipUserDataSecretPropagation(nodePool *hyperv1.NodePool, targetConfigHash, targetVersion, currentTemplateVersion string) bool {
	if nodePool == nil {
		return false
	}
	return targetConfigHash == nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig] &&
		targetVersion == currentTemplateVersion
}
