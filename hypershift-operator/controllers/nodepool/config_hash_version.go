package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	// ConfigHashVersionV1 is the NodePool config hash formula on main before trust-bundle
	// ConfigMap content was included. It hashed the additionalTrustBundle ConfigMap name
	// (not ca-bundle.crt content) and did not include proxy.trustedCA content.
	ConfigHashVersionV1 = "v1"
	// ConfigHashVersionV2 hashes additionalTrustBundle and proxy.trustedCA ConfigMap content.
	ConfigHashVersionV2 = "v2"
	// CurrentConfigHashVersion is the hash formula used for new rollouts and migrations.
	CurrentConfigHashVersion = ConfigHashVersionV2
)

type configHashReconcileOutcome struct {
	AnnotationsUpdated    bool
	ConfigActuallyChanged bool
	VersionMigrated       bool
}

func storedConfigHashVersion(nodePool *hyperv1.NodePool) string {
	if nodePool == nil {
		return ConfigHashVersionV1
	}
	if version := nodePool.GetAnnotations()[nodePoolAnnotationConfigHashVersion]; version != "" {
		return version
	}
	return ConfigHashVersionV1
}

func writeConfigHashAnnotations(nodePool *hyperv1.NodePool, hashWithoutVersion, hashWithVersion string) {
	if nodePool.Annotations == nil {
		nodePool.Annotations = make(map[string]string)
	}
	nodePool.Annotations[nodePoolAnnotationCurrentConfig] = hashWithoutVersion
	nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = hashWithVersion
	nodePool.Annotations[nodePoolAnnotationConfigHashVersion] = CurrentConfigHashVersion
}

// reconcileConfigHashAnnotations compares the NodePool's stored config hash against the hash
// computed at the stored formula version. When only the hash formula changed (operator upgrade),
// it rewrites annotations to the current version without signaling a config rollout. When the
// stored hash no longer matches the stored version's formula, it treats that as a real config
// change and updates annotations at the current version.
func reconcileConfigHashAnnotations(nodePool *hyperv1.NodePool, cg *ConfigGenerator) configHashReconcileOutcome {
	if nodePool == nil || cg == nil || cg.rolloutConfig == nil || cg.hostedCluster == nil || cg.releaseImage == nil {
		return configHashReconcileOutcome{}
	}

	annotations := nodePool.GetAnnotations()
	storedHashWithoutVersion := annotations[nodePoolAnnotationCurrentConfig]
	if storedHashWithoutVersion == "" {
		return configHashReconcileOutcome{}
	}

	storedVersion := storedConfigHashVersion(nodePool)
	storedHashWithVersion := annotations[nodePoolAnnotationCurrentConfigVersion]

	hashAtStoredVersion := cg.hashWithoutVersionAtVersion(storedVersion)
	hashFullAtStoredVersion := cg.hashAtVersion(storedVersion)

	configActuallyChanged := storedHashWithoutVersion != hashAtStoredVersion ||
		(storedHashWithVersion != "" && storedHashWithVersion != hashFullAtStoredVersion)

	newHashWithoutVersion := cg.HashWithoutVersion()
	newHashWithVersion := cg.Hash()

	if configActuallyChanged {
		writeConfigHashAnnotations(nodePool, newHashWithoutVersion, newHashWithVersion)
		return configHashReconcileOutcome{
			AnnotationsUpdated:    true,
			ConfigActuallyChanged: true,
		}
	}

	if storedVersion != CurrentConfigHashVersion {
		writeConfigHashAnnotations(nodePool, newHashWithoutVersion, newHashWithVersion)
		return configHashReconcileOutcome{
			AnnotationsUpdated: true,
			VersionMigrated:    true,
		}
	}

	return configHashReconcileOutcome{}
}

// shouldSkipUserDataSecretPropagation reports whether a differing user-data Secret name should
// not be written to the MachineDeployment/MachineSet. After a hash-formula version migration
// rewrites NodePool annotations, config and version targets already match the baseline while the
// Secret name still encodes the previous hash. Rewriting DataSecretName in that case would spuriously
// roll Nodes.
func shouldSkipUserDataSecretPropagation(nodePool *hyperv1.NodePool, targetConfigHash, targetVersion, currentTemplateVersion string) bool {
	if nodePool == nil {
		return false
	}
	return targetConfigHash == nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig] &&
		targetVersion == currentTemplateVersion
}
