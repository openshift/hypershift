package util

import (
	"context"
	"fmt"
	"os"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/blang/semver"
)

var (
	// y-stream versions supported by e2e in main
	Version51  = semver.MustParse("5.1.0")
	Version50  = semver.MustParse("5.0.0")
	Version423 = semver.MustParse("4.23.0")
	Version422 = semver.MustParse("4.22.0")
	Version421 = semver.MustParse("4.21.0")
	Version420 = semver.MustParse("4.20.0")
	Version419 = semver.MustParse("4.19.0")
	Version418 = semver.MustParse("4.18.0")
	Version417 = semver.MustParse("4.17.0")
	Version416 = semver.MustParse("4.16.0")
	Version415 = semver.MustParse("4.15.0")
	Version414 = semver.MustParse("4.14.0")
)

func GetReleaseImageVersion(ctx context.Context, releaseImage string, pullSecretFile string) (semver.Version, error) {
	data, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return semver.Version{}, fmt.Errorf("error reading file: %w", err)
	}
	releaseInfoProvider := releaseinfo.RegistryClientProvider{}
	releaseImageInfo, err := releaseInfoProvider.Lookup(ctx, releaseImage, data)
	if err != nil {
		return semver.Version{}, fmt.Errorf("error looking up latest release image: %w", err)
	}
	version, err := semver.Parse(releaseImageInfo.Version())
	if err != nil {
		return semver.Version{}, fmt.Errorf("error parsing version: %w", err)
	}
	version.Patch = 0
	version.Pre = nil
	version.Build = nil
	return version, nil
}

func AtLeast(t *testing.T, releaseVersion semver.Version, version semver.Version) {
	if releaseVersion.LT(version) {
		t.Skipf("Only tested in %s and later", version)
	}
}

func CPOAtLeast(t *testing.T, releaseVersion semver.Version, version semver.Version, hc *hyperv1.HostedCluster) {
	if hc.Status.Version == nil || hc.Status.Version.Desired.Version == "" {
		t.Logf("Desired version is not set on the HostedCluster using latestReleaseImage: %s", releaseVersion)
		AtLeast(t, releaseVersion, version)
	}
	cpoVersion := semver.MustParse(hc.Status.Version.Desired.Version)
	if cpoVersion.LT(version) {
		t.Skipf("Only tested in %s and later", version)
	}
}

func IsLessThan(releaseVersion semver.Version, version semver.Version) bool {
	return releaseVersion.LT(version)
}

func IsGreaterThanOrEqualTo(releaseVersion semver.Version, version semver.Version) bool {
	return releaseVersion.GE(version)
}

// ShouldRunKarpenterTests skips the test unless the Karpenter v1 API is available.
// The v1 API exists on 4.23+, but when the operator is built from main and
// tested against a 4.22 hosted cluster, set RUN_KARPENTER_TESTS=true to
// lower the gate to 4.22.
func ShouldRunKarpenterTests(t *testing.T, releaseVersion semver.Version) {
	if os.Getenv("RUN_KARPENTER_TESTS") == "true" {
		AtLeast(t, releaseVersion, Version422)
	} else {
		AtLeast(t, releaseVersion, Version423)
	}
}
