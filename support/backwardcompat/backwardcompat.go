package backwardcompat

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

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

// GetBackwardCompatibleCAPIImage returns a CAPI image pinned to a CAPI 1.10 compatible version.
// If the releaseVersion is 4.21 or higher. Otherwise an empty string is returned.
func GetBackwardCompatibleCAPIImage(ctx context.Context, pullSecret []byte, releaseProvider releaseinfo.Provider, releaseVersion semver.Version, component string) (string, error) {
	// TODO(https://issues.redhat.com/browse/CNTRLPLANE-1200): Remove this override once Hypershift installs the CAPI v1beta2 API version
	// temporary override for 4.21 to unblock CAPI bump to 1.11 which introduces a new API version.
	// The images returned are pinned to version 4.20.10.
	const (
		pinnedRelease = "quay.io/openshift-release-dev/ocp-release@sha256:7f183e9b5610a2c9f9aabfd5906b418adfbe659f441b019933426a19bf6a5962"
		minRelease    = "4.21.0-0"
	)

	if releaseVersion.GTE(semver.MustParse(minRelease)) {
		imageOverride, err := supportutil.GetPayloadImageFromRelease(ctx, releaseProvider, pinnedRelease, component, pullSecret)
		if err != nil {
			return "", fmt.Errorf("error getting backwards compatible image for %s:%s: %w", component, pinnedRelease, err)
		}

		return imageOverride, nil
	}

	return "", nil
}
