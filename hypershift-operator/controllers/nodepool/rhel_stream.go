package nodepool

import (
	"fmt"

	"github.com/blang/semver"
)

var rhel10MinVersion = semver.Version{Major: 5, Minor: 0, Patch: 0}

const (
	RHELStream9  = "rhel-9"
	RHELStream10 = "rhel-10"
)

// getRHELStream resolves the effective RHEL stream for a NodePool.
//
// specStream is the user-specified stream from spec.osImageStream.name (may be "").
// releaseVersion is the parsed semver of the release image.
// usesRunc indicates whether a ContainerRuntimeConfig with defaultRuntime=runc is present.
//
// Returns (stream, fallbackMessage, error):
//   - stream: the resolved stream name ("rhel-9", "rhel-10"), or "" for legacy (pre-5.0, unset).
//   - fallbackMessage: non-empty when an implicit resolution fell back (e.g. runc forced rhel-9).
//   - error: non-nil for invalid explicit configurations that should be rejected.
func getRHELStream(specStream string, releaseVersion semver.Version, usesRunc bool) (stream string, fallbackMsg string, err error) {
	isExplicit := specStream != ""
	isGTE5 := releaseVersion.GTE(rhel10MinVersion)

	if isExplicit {
		if specStream == RHELStream10 && !isGTE5 {
			return "", "", fmt.Errorf(
				"OS stream %s requires release version >= 5.0, got %s", RHELStream10, releaseVersion)
		}
		if specStream == RHELStream10 && usesRunc {
			return "", "", fmt.Errorf(
				"OS stream %s is incompatible with default_runtime=runc; RHEL 10 does not ship runc", RHELStream10)
		}
		return specStream, "", nil
	}

	if isGTE5 {
		if usesRunc {
			return RHELStream9, "OS stream defaulted to rhel-9: RHEL 10 is incompatible with default_runtime=runc", nil
		}
		return RHELStream10, "", nil
	}

	return "", "", nil
}
