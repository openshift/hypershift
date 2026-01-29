package backwardcompat

import (
	"bytes"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sigyaml "sigs.k8s.io/yaml"
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
