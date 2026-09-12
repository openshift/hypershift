package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// Config hash version migration flow
//
// When the HyperShift Operator upgrades from name-based to content-based trust-bundle
// hashing, three components coordinate to avoid spuriously rolling worker Nodes:
//
//  1. reconcileConfigHashAnnotations (called early in reconcile, before CAPI)
//     Compares the NodePool's stored hash against the hash computed at the stored formula
//     version. When only the hash formula changed (v1→v2), it rewrites annotations to the
//     current version without signaling a config rollout. After this step, the NodePool
//     annotations match what ConfigGenerator.Hash/HashWithoutVersion produce — so the
//     controller considers config "up to date" and does not set UpdatingConfig.
//     The outcome is passed to CAPI via SetConfigHashReconcileOutcome.
//
//  2. shouldSkipUserDataSecretPropagation (called in CAPI propagateVersionAndTemplate)
//     User-data Secret names embed the config hash (e.g. "user-data-workers-<hash>").
//     After step 1, the newly computed Secret name differs from the one still referenced
//     by the MachineDeployment/MachineSet, even though no real config changed — only the
//     hash formula did. When VersionMigrated is set, or when the target config and version
//     already match the NodePool baseline, this function suppresses the Secret name update,
//     preventing CAPI from replacing Machines.
//
//  3. secretJanitor.liveBootstrapSecretNames (called during Secret cleanup)
//     During the migration window the MachineDeployment/MachineSet still references the
//     old user-data and token Secrets (step 2 intentionally left them in place). The
//     janitor looks up the live DataSecretName from the MachineDeployment/MachineSet and
//     preserves those Secrets from garbage collection until a real config change moves
//     DataSecretName forward.
//
// After a subsequent real ca-bundle.crt content change:
//   - reconcileConfigHashAnnotations returns ConfigActuallyChanged without rewriting annotations.
//   - shouldSkipUserDataSecretPropagation returns false (config hash mismatch).
//   - The controller propagates the new Secret name normally and rolls Nodes.
//   - The janitor cleans up the now-unreferenced old Secrets.

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
// stored hash no longer matches the stored version's formula, it signals a real config change
// and leaves annotations unchanged so the normal rollout path can update them after propagation.
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
		return configHashReconcileOutcome{
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
func shouldSkipUserDataSecretPropagation(nodePool *hyperv1.NodePool, outcome configHashReconcileOutcome, targetConfigHash, targetVersion, currentTemplateVersion string) bool {
	if nodePool == nil {
		return false
	}
	if outcome.VersionMigrated {
		return true
	}
	return configHashBaselineMatchesTarget(nodePool, targetConfigHash, targetVersion, currentTemplateVersion)
}

func configHashBaselineMatchesTarget(nodePool *hyperv1.NodePool, targetConfigHash, targetVersion, currentTemplateVersion string) bool {
	if nodePool == nil {
		return false
	}
	return targetConfigHash == nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfig] &&
		targetVersion == currentTemplateVersion
}
