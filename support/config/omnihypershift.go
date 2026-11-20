package config

import (
	"fmt"
	"os"

	"github.com/blang/semver"
)

// Omni HyperShift lets a single main-built Control Plane Operator manage a set of older
// OpenShift control planes. It is activated per-HostedControlPlane by the HyperShift
// Operator, which sets OmniHypershiftEnvVar on the CPO deployment only when it selected a
// from-main CPO image for that cluster (see hypershift-operator/omnihypershift). When off
// (the default) every component renders exactly as before, so a CPO that was not deployed
// from main is unaffected.
const OmniHypershiftEnvVar = "OMNI_HYPERSHIFT"

// omniHypershiftSupportedVersions is the set of control-plane MAJOR.MINOR versions this CPO
// build knows how to manage from main. It must equal the set actually exercised by the
// e2e matrix: add a version here only once a matrix leg proves it works.
var omniHypershiftSupportedVersions = []semver.Version{
	Version420,
	Version421,
	Version422,
	Version423,
	Version500,
}

// IsOmniHypershiftEnabled reports whether this CPO process is running in Omni HyperShift mode.
func IsOmniHypershiftEnabled() bool {
	return os.Getenv(OmniHypershiftEnvVar) == "1"
}

// OmniHypershiftSupportsVersion reports whether Omni HyperShift can manage the given control-plane
// version, compared at MAJOR.MINOR.
func OmniHypershiftSupportsVersion(version semver.Version) bool {
	for _, v := range omniHypershiftSupportedVersions {
		if version.Major == v.Major && version.Minor == v.Minor {
			return true
		}
	}
	return false
}

// OmniHypershiftCheckVersion returns an error when Omni HyperShift is enabled but cannot manage the
// given control-plane payload version. It is a no-op when Omni HyperShift is off, so it is safe
// to call unconditionally early in reconciliation.
func OmniHypershiftCheckVersion(controlPlaneVersion string) error {
	if !IsOmniHypershiftEnabled() {
		return nil
	}
	v, err := semver.Parse(controlPlaneVersion)
	if err != nil {
		return fmt.Errorf("cannot parse control-plane version %q for Omni HyperShift: %w", controlPlaneVersion, err)
	}
	if !OmniHypershiftSupportsVersion(v) {
		return fmt.Errorf("this control-plane-operator build does not support managing OpenShift %d.%d from main with Omni HyperShift", v.Major, v.Minor)
	}
	return nil
}
