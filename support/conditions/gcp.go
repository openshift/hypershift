package conditions

import "github.com/blang/semver"

// SupportsGCPRuntimeCredentialValidation reports whether the management-side control
// plane version supports runtime GCP credential validation. An empty or malformed
// semantic version is unknown. Patch, prerelease, and build metadata do not affect support.
func SupportsGCPRuntimeCredentialValidation(version string) (supported bool, known bool) {
	parsed, err := semver.Parse(version)
	if err != nil {
		return false, false
	}
	return parsed.Major > 5 || (parsed.Major == 5 && parsed.Minor >= 1), true
}

// SupportsGCPResourceTags reports whether the management-side control plane
// version supports propagating GCP resource tags to guest Infrastructure status.
// An empty or malformed semantic version is unknown. Patch, prerelease, and
// build metadata do not affect support.
func SupportsGCPResourceTags(version string) (supported bool, known bool) {
	parsed, err := semver.Parse(version)
	if err != nil {
		return false, false
	}
	return parsed.Major > 5 || (parsed.Major == 5 && parsed.Minor >= 3), true
}
