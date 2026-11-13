package config

import (
	"fmt"
	"os"

	"github.com/blang/semver"
)

// OMNI-CPO lets a single main-built Control Plane Operator manage a set of older
// OpenShift control planes. It is activated per-HostedControlPlane by the HyperShift
// Operator, which sets OmniCPOEnvVar on the CPO deployment only when it selected a
// from-main CPO image for that cluster (see
// hypershift-operator/controlplaneoperator-overrides). When off (the default) every
// component renders exactly as before, so a CPO that was not deployed from main is
// unaffected.
const OmniCPOEnvVar = "OMNI_CPO"

// omniCPOSupportedVersions is the set of control-plane MAJOR.MINOR versions this CPO
// build knows how to manage from main. It must equal the set actually exercised by the
// e2e matrix: add a version here only once a matrix leg proves it works.
var omniCPOSupportedVersions = []semver.Version{
	Version420,
	Version421,
	Version422,
	Version423,
	Version500,
}

// IsOmniCPOEnabled reports whether this CPO process is running in OMNI-CPO mode.
func IsOmniCPOEnabled() bool {
	return os.Getenv(OmniCPOEnvVar) == "1"
}

// OmniCPOSupportsVersion reports whether OMNI-CPO can manage the given control-plane
// version, compared at MAJOR.MINOR.
func OmniCPOSupportsVersion(version semver.Version) bool {
	for _, v := range omniCPOSupportedVersions {
		if version.Major == v.Major && version.Minor == v.Minor {
			return true
		}
	}
	return false
}

// OmniCPOCheckVersion returns an error when OMNI-CPO is enabled but cannot manage the
// given control-plane payload version. It is a no-op when OMNI-CPO is off, so it is safe
// to call unconditionally early in reconciliation.
func OmniCPOCheckVersion(controlPlaneVersion string) error {
	if !IsOmniCPOEnabled() {
		return nil
	}
	v, err := semver.Parse(controlPlaneVersion)
	if err != nil {
		return fmt.Errorf("OMNI-CPO: cannot parse control-plane version %q: %w", controlPlaneVersion, err)
	}
	if !OmniCPOSupportsVersion(v) {
		return fmt.Errorf("OMNI-CPO: this control-plane-operator build does not support managing OpenShift %d.%d from main", v.Major, v.Minor)
	}
	return nil
}
