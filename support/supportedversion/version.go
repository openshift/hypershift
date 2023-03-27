package supportedversion

import (
	"fmt"
	"strings"

	"github.com/blang/semver"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

// LatestSupportedVersion is the latest minor OCP version supported by the
// HyperShift operator.
// NOTE: The .0 (z release) should be ignored. It's only here to support
// semver parsing.
var LatestSupportedVersion = semver.MustParse("4.14.0")
var MinSupportedVersion = semver.MustParse(subtractMinor(&LatestSupportedVersion, uint64(SupportedPreviousMinorVersions)).String())

// SupportedPreviousMinorVersions is the number of minor versions prior to current
// version that are supported.
const SupportedPreviousMinorVersions = 2

func Supported() []string {
	versions := []string{trimVersion(LatestSupportedVersion.String())}
	for i := 0; i < SupportedPreviousMinorVersions; i++ {
		versions = append(versions, trimVersion(subtractMinor(&LatestSupportedVersion, uint64(i+1)).String()))
	}
	return versions
}

func trimVersion(version string) string {
	return strings.TrimSuffix(version, ".0")
}

func subtractMinor(version *semver.Version, count uint64) *semver.Version {
	result := *version
	result.Minor = maxInt64(0, result.Minor-count)
	return &result
}

func maxInt64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func IsValidReleaseVersion(version, currentVersion, latestVersionSupported, minSupportedVersion *semver.Version, networkType hyperv1.NetworkType, platformType hyperv1.PlatformType) error {
	if version.LT(semver.MustParse("4.8.0")) {
		return fmt.Errorf("releases before 4.8 are not supported. Attempting to use: %q", version)
	}

	if currentVersion != nil && currentVersion.Minor > version.Minor {
		return fmt.Errorf("y-stream downgrade from %q to %q is not supported", currentVersion, version)
	}

	if networkType == hyperv1.OpenShiftSDN && currentVersion != nil && currentVersion.Minor < version.Minor {
		return fmt.Errorf("y-stream upgrade from %q to %q is not for OpenShiftSDN", currentVersion, version)
	}

	versionMinorOnly := &semver.Version{Major: version.Major, Minor: version.Minor}
	if networkType == hyperv1.OpenShiftSDN && currentVersion == nil && versionMinorOnly.GT(semver.MustParse("4.10.0")) && platformType != hyperv1.PowerVSPlatform {
		return fmt.Errorf("cannot use OpenShiftSDN with OCP version %q > 4.10", version)
	}

	if networkType == hyperv1.OVNKubernetes && currentVersion == nil && versionMinorOnly.LTE(semver.MustParse("4.10.0")) {
		return fmt.Errorf("cannot use OVNKubernetes with OCP version %q < 4.11", version)
	}

	if (version.Major == latestVersionSupported.Major && version.Minor > latestVersionSupported.Minor) || version.Major > latestVersionSupported.Major {
		return fmt.Errorf("the latest version supported is: %q. Attempting to use: %q", LatestSupportedVersion, version)
	}

	if (version.Major == minSupportedVersion.Major && version.Minor < minSupportedVersion.Minor) || version.Major < minSupportedVersion.Major {
		return fmt.Errorf("the minimum version supported is: %q. Attempting to use: %q", MinSupportedVersion, version)
	}

	return nil
}
