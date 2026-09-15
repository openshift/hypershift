package backwardcompat

import (
	"bytes"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sigyaml "sigs.k8s.io/yaml"

	"github.com/blang/semver"
)

const ImageStreamImportModeField = "imageStreamImportMode"

// GetBackwardCompatibleConfigString returns a ClusterConfiguration which is backward-compatible with older CPO versions
func GetBackwardCompatibleConfigString(stringData string) string {
	// This PR https://github.com/openshift/api/pull/1928 introduced a string field which has no omitempty tag.
	// This results in our mashaling transparently changing. This produces a different configuration Hash.
	// This drops the field when it shows up as empty in the mashaled string to keep backward compatibility.
	// Implementing this at the marshal operation level might result in undesired impact as we might potentially modify other fields and ordering is not deterministic
	return supportutil.RemoveEmptyJSONField(stringData, ImageStreamImportModeField)
}

// GetBackwardCompatibleConfigHash returns a hash of ClusterConfiguration which is backward-compatible with CPO versions that doesn't
// hash the ImageSpec.
func GetBackwardCompatibleConfigHash(config *v1beta1.ClusterConfiguration) (string, error) {
	// This PR https://github.com/openshift/api/pull/1928 introduced a string field which has no omitempty tag.
	// This results in our mashaling transparently changing. This produces a different configuration Hash.
	// We need to drop the field when it shows up as empty in the marshaled string to keep backward compatibility.
	// Implementing this at the marshal operation level might result in undesired impact as we might potentially modify other fields and ordering is not deterministic
	return supportutil.HashStructWithJSONMapper(config, supportutil.NewOmitFieldIfEmptyJSONMapper(ImageStreamImportModeField))
}

// GetBackwardCompatibleCAPIImage returns a CAPI image pinned to a version that
// writes status through the v1beta2 API. Payloads at 4.18 or below ship a CAPI
// controller that writes status via v1beta1; the v1beta1→v1beta2 conversion
// drops the phase field, leaving it permanently empty.
// The pinned image is from OCP 4.22 which includes CAPI 1.13. It is the
// cluster-capi-controllers component from the pinned OCP release payload
// quay.io/openshift-release-dev/ocp-release@sha256:1dbbdfdde4bb3f3ed4bca965e810a2990a3913990fb4a57072764b771f604554.
// Keep this as the component image rather than the release payload so
// reconciliation does not need to look up the entire pinned payload.
func GetBackwardCompatibleCAPIImage(releaseVersion semver.Version) string {
	const (
		backwardCompatibleCAPIImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:c5c3e36db897fae332284e1a681044cd6997a70e8cb9059f810385352e7575ac"
		minUnaffectedVersion        = "4.19.0-0"
	)

	releaseVersion.Pre = nil
	if releaseVersion.LT(semver.MustParse(minUnaffectedVersion)) {
		return backwardCompatibleCAPIImage
	}

	return ""
}

// NormalizeV1Alpha1ClusterImagePolicy rewrites the apiVersion of ClusterImagePolicy
// manifests from config.openshift.io/v1alpha1 to config.openshift.io/v1.
//
// The ClusterImagePolicy type was promoted from v1alpha1 to v1 in openshift/api and the
// Go type no longer exists in the v1alpha1 package. Without this normalization, existing
// NodePools that reference v1alpha1 ClusterImagePolicy configs would fail to decode after
// a HyperShift Operator upgrade, breaking reconciliation for those clusters.
func NormalizeV1Alpha1ClusterImagePolicy(manifest []byte) []byte {
	var meta metav1.TypeMeta
	if err := sigyaml.Unmarshal(manifest, &meta); err != nil {
		return manifest
	}
	if meta.APIVersion == "config.openshift.io/v1alpha1" && meta.Kind == "ClusterImagePolicy" {
		manifest = bytes.Replace(manifest, []byte("config.openshift.io/v1alpha1"), []byte("config.openshift.io/v1"), 1)
	}
	return manifest
}
