package nodepool

import (
	"fmt"

	"github.com/blang/semver"
)

// getRHELStream resolves the effective RHEL stream for a NodePool.
// specStream is the value from spec.osImageStream.name (empty string when unset).
// releaseVersion is the parsed semantic version of the NodePool's release image.
// usesRunc indicates whether the NodePool config references a ContainerRuntimeConfig with defaultRuntime=runc.
//
// Decision logic:
//   - Explicit "rhel-10" + runc → error (RHEL 10 does not ship runc)
//   - Explicit "rhel-10" + release < 5.0 → error (RHEL 10 not available before 5.0)
//   - Explicit value → return as-is
//   - Unset + >= 5.0 + runc → return "rhel-9" (fallback)
//   - Unset + >= 5.0 → return "rhel-10" (default)
//   - Unset + < 5.0 → return "" (no stream, legacy behavior)
func getRHELStream(specStream string, releaseVersion semver.Version, usesRunc bool) (string, error) {
	version5 := semver.Version{Major: 5, Minor: 0, Patch: 0}

	if specStream == "rhel-10" {
		if usesRunc {
			return "", fmt.Errorf("RHEL 10 does not ship runc; remove the ContainerRuntimeConfig with defaultRuntime=runc or use rhel-9")
		}
		if releaseVersion.LT(version5) {
			return "", fmt.Errorf("RHEL 10 requires release version >= 5.0, current version is %s", releaseVersion.String())
		}
		return "rhel-10", nil
	}

	if specStream == "rhel-9" {
		return "rhel-9", nil
	}

	// specStream is unset - derive default from release version
	if releaseVersion.GTE(version5) {
		if usesRunc {
			return "rhel-9", nil
		}
		return "rhel-10", nil
	}

	// < 5.0, no stream (legacy behavior)
	return "", nil
}
