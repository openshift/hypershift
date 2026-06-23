package nodepool

import (
	"fmt"

	"github.com/blang/semver"
)

const (
	// RHELStreamRHEL9 is the RHEL 9 OS stream identifier.
	RHELStreamRHEL9 = "rhel-9"
	// RHELStreamRHEL10 is the RHEL 10 OS stream identifier.
	RHELStreamRHEL10 = "rhel-10"
)

// rhelStreamMinVersion is the minimum release version that supports RHEL 10.
// Releases before this version only support RHEL 9.
var rhelStreamMinVersion = semver.Version{Major: 5, Minor: 0, Patch: 0}

// getRHELStream resolves the OS stream for a NodePool based on the user's explicit
// spec selection, the release version, and whether the NodePool uses runc.
//
// Rules:
//   - If specStream is set to "rhel-10" but the release is < 5.0, return an error.
//   - If specStream is set to "rhel-10" but the NodePool uses runc, return an error (RHEL 10 drops runc).
//   - If specStream is set explicitly and is valid, return it.
//   - If specStream is empty and release < 5.0, return "" (legacy behavior, no OSImageStream CR generated).
//   - If specStream is empty and release >= 5.0 and usesRunc, fall back to "rhel-9".
//   - If specStream is empty and release >= 5.0, default to "rhel-10".
func getRHELStream(specStream string, releaseVersion semver.Version, usesRunc bool) (string, error) {
	isPost5 := releaseVersion.GTE(rhelStreamMinVersion)

	switch specStream {
	case RHELStreamRHEL10:
		if !isPost5 {
			return "", fmt.Errorf("OS stream %q is not supported for release version %s (requires >= %s)", specStream, releaseVersion, rhelStreamMinVersion)
		}
		if usesRunc {
			return "", fmt.Errorf("OS stream %q is not compatible with ContainerRuntimeConfig using runc: RHEL 10 does not support runc", specStream)
		}
		return RHELStreamRHEL10, nil

	case RHELStreamRHEL9:
		return RHELStreamRHEL9, nil

	case "":
		// No explicit stream selected — derive from release version.
		if !isPost5 {
			// Pre-5.0: legacy behavior, no OSImageStream CR generated.
			return "", nil
		}
		// >= 5.0: default to rhel-10, but fall back to rhel-9 if runc is in use.
		if usesRunc {
			return RHELStreamRHEL9, nil
		}
		return RHELStreamRHEL10, nil

	default:
		return "", fmt.Errorf("unsupported OS stream %q: must be %q or %q", specStream, RHELStreamRHEL9, RHELStreamRHEL10)
	}
}
