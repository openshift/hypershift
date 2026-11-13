package controlplaneoperatoroverrides

import (
	"fmt"
	"os"
	"strings"

	"github.com/blang/semver"
)

// This package resolves "a Control Plane Operator image other than the one shipped in
// the guest's release payload". It serves two features through the single seam
// support/imageregistry.GetControlPlaneOperatorImage:
//
//   - static hotfix overrides: an embedded per-version image table (overrides.go),
//     gated by ENABLE_CPO_OVERRIDES; and
//   - CPO-from-main (this file): drive the CPO from the HyperShift Operator's own main
//     build for a configured set of releases, gated by ENABLE_CPO_FROM_MAIN.

const (
	// CPOFromMainEnvVar, when "1" on the HyperShift Operator, enables serving the CPO
	// from main for the releases listed in CPOFromMainVersionsEnvVar.
	CPOFromMainEnvVar = "ENABLE_CPO_FROM_MAIN"

	// CPOFromMainVersionsEnvVar is a comma-separated list of MAJOR.MINOR OpenShift
	// releases for which the CPO is driven from main (e.g. "4.20" or "4.20,4.21").
	CPOFromMainVersionsEnvVar = "CPO_FROM_MAIN_VERSIONS"

	// CPOFromMainImageEnvVar is the CPO image to use for from-main releases. When empty,
	// the HyperShift Operator's own image is used, which is the main CPO.
	CPOFromMainImageEnvVar = "CPO_FROM_MAIN_IMAGE"
)

// IsFromMainEnabled reports whether CPO-from-main is enabled on the HyperShift Operator.
func IsFromMainEnabled() bool {
	return os.Getenv(CPOFromMainEnvVar) == "1"
}

// FromMainVersions returns the configured set of MAJOR.MINOR releases, keyed for lookup.
func FromMainVersions() map[string]struct{} {
	versions := map[string]struct{}{}
	for _, v := range strings.Split(os.Getenv(CPOFromMainVersionsEnvVar), ",") {
		if v = strings.TrimSpace(v); v != "" {
			versions[v] = struct{}{}
		}
	}
	return versions
}

// FromMainImage returns the explicitly configured from-main CPO image, or "" when none
// is set (the caller then falls back to the HyperShift Operator's own image).
func FromMainImage() string {
	return strings.TrimSpace(os.Getenv(CPOFromMainImageEnvVar))
}

// CPOFromMainImage returns the CPO image to use for the given release when CPO-from-main
// is enabled and the release's MAJOR.MINOR is in the configured set. It returns "" when
// from-main does not apply, so the caller falls through to the normal resolution order.
// When from-main applies but no explicit image is configured, hypershiftOperatorImage
// (the main CPO) is returned.
// ShouldUseFromMain reports whether CPO-from-main applies to the given release version:
// the feature is enabled and the version's MAJOR.MINOR is in CPO_FROM_MAIN_VERSIONS.
func ShouldUseFromMain(version semver.Version) bool {
	if !IsFromMainEnabled() {
		return false
	}
	_, ok := FromMainVersions()[majorMinor(version)]
	return ok
}

func CPOFromMainImage(version semver.Version, hypershiftOperatorImage string) string {
	if !ShouldUseFromMain(version) {
		return ""
	}
	if image := FromMainImage(); image != "" {
		return image
	}
	return hypershiftOperatorImage
}

func majorMinor(version semver.Version) string {
	return fmt.Sprintf("%d.%d", version.Major, version.Minor)
}
