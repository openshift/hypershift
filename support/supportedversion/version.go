package supportedversion

import (
	"strings"

	"github.com/coreos/go-semver/semver"
)

// LatestSupportedVersion is the latest minor OCP version supported by the
// HyperShift operator.
// NOTE: The .0 (z release) should be ignored. It's only here to support
// semver parsing.
var LatestSupportedVersion = semver.New("4.12.0")

// SupportedPreviousMinorVersions is the number of minor versions prior to current
// version that are supported.
const SupportedPreviousMinorVersions = 2

func Supported() []string {
	versions := []string{trimVersion(LatestSupportedVersion.String())}
	for i := 0; i < SupportedPreviousMinorVersions; i++ {
		versions = append(versions, trimVersion(subtractMinor(LatestSupportedVersion, int64(i+1)).String()))
	}
	return versions
}

func trimVersion(version string) string {
	return strings.TrimSuffix(version, ".0")
}

func subtractMinor(version *semver.Version, count int64) *semver.Version {
	result := *version
	result.Minor = maxInt64(0, result.Minor-count)
	return &result
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
