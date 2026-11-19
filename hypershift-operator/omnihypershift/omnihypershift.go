package omnihypershift

import (
	"fmt"
	"os"
	"strings"

	"github.com/blang/semver"
)

// Package omnihypershift is the HyperShift-Operator side of Omni HyperShift. It decides whether the
// Control Plane Operator should be driven from the HyperShift Operator's own main build, instead of
// the CPO shipped in the guest's release payload, for a configured set of releases. It is consumed
// through the single seam support/imageregistry.GetControlPlaneOperatorImage. The CPO-side
// compatibility engine lives in support/omnihypershift and support/config.

const (
	// EnableEnvVar, when "1" on the HyperShift Operator, enables Omni HyperShift for the releases
	// listed in VersionsEnvVar.
	EnableEnvVar = "ENABLE_OMNI_HYPERSHIFT"

	// VersionsEnvVar is a comma-separated list of MAJOR.MINOR OpenShift releases Omni HyperShift
	// serves from main (e.g. "4.20" or "4.20,4.21").
	VersionsEnvVar = "OMNI_HYPERSHIFT_VERSIONS"

	// ImageEnvVar is the CPO image to use for Omni HyperShift releases. When empty, the HyperShift
	// Operator's own image is used, which is the main CPO.
	ImageEnvVar = "OMNI_HYPERSHIFT_IMAGE"
)

// IsEnabled reports whether Omni HyperShift is enabled on the HyperShift Operator.
func IsEnabled() bool {
	return os.Getenv(EnableEnvVar) == "1"
}

// Versions returns the configured set of MAJOR.MINOR releases, keyed for lookup.
func Versions() map[string]struct{} {
	versions := map[string]struct{}{}
	for _, v := range strings.Split(os.Getenv(VersionsEnvVar), ",") {
		if v = strings.TrimSpace(v); v != "" {
			versions[v] = struct{}{}
		}
	}
	return versions
}

// Image returns the explicitly configured Omni HyperShift CPO image, or "" when none is set (the
// caller then falls back to the HyperShift Operator's own image).
func Image() string {
	return strings.TrimSpace(os.Getenv(ImageEnvVar))
}

// ShouldUse reports whether Omni HyperShift applies to the given release version: the feature is
// enabled and the version's MAJOR.MINOR is in OMNI_HYPERSHIFT_VERSIONS.
func ShouldUse(version semver.Version) bool {
	if !IsEnabled() {
		return false
	}
	_, ok := Versions()[majorMinor(version)]
	return ok
}

// CPOImage returns the CPO image to use for the given release when Omni HyperShift is enabled and the
// release's MAJOR.MINOR is in the configured set. It returns "" when Omni HyperShift does not apply,
// so the caller falls through to the normal resolution order. When it applies but no explicit image
// is configured, hypershiftOperatorImage (the main CPO) is returned.
func CPOImage(version semver.Version, hypershiftOperatorImage string) string {
	if !ShouldUse(version) {
		return ""
	}
	if image := Image(); image != "" {
		return image
	}
	return hypershiftOperatorImage
}

func majorMinor(version semver.Version) string {
	return fmt.Sprintf("%d.%d", version.Major, version.Minor)
}
