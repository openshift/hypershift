package backwardcompat

import (
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"
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
