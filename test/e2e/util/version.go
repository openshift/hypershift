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
	Version420 = semver.MustParse("4.20.0")
	Version419 = semver.MustParse("4.19.0")
	Version418 = semver.MustParse("4.18.0")
	Version417 = semver.MustParse("4.17.0")
	Version416 = semver.MustParse("4.16.0")
	Version415 = semver.MustParse("4.15.0")
	Version414 = semver.MustParse("4.14.0")

	releaseVersion semver.Version
)

func init() {
	// Ensure that the version constants are valid semver versions
	// This is a compile-time check to ensure that the versions are valid
	// semver versions.
	_ = Version420
	_ = Version419
	_ = Version418
	_ = Version417
	_ = Version416
	_ = Version415
	_ = Version414
}

func SetReleaseImageVersion(ctx context.Context, latestReleaseImage string, pullSecretFile string) error {
	data, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return fmt.Errorf("error reading file: %v", err)
	}
	releaseInfoProvider := releaseinfo.RegistryClientProvider{}
	releaseImage, err := releaseInfoProvider.Lookup(ctx, latestReleaseImage, data)
	if err != nil {
		return fmt.Errorf("error looking up latest release image: %v", err)
	}
	releaseVersion, err = semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("error parsing version: %v", err)
	}
	releaseVersion.Patch = 0
	releaseVersion.Pre = nil
	releaseVersion.Build = nil
	return nil
}

func AtLeast(t *testing.T, version semver.Version) {
	if releaseVersion.LT(version) {
		t.Skipf("Only tested in %s and later", version)
	}
}

func CPOAtLeast(t *testing.T, version semver.Version, hc *hyperv1.HostedCluster) {
	if hc.Status.Version == nil || hc.Status.Version.Desired.Version == "" {
		t.Logf("Desired version is not set on the HostedCluster using latestReleaseImage: %s", releaseVersion)
		AtLeast(t, version)
	}
	cpoVersion := semver.MustParse(hc.Status.Version.Desired.Version)
	if cpoVersion.LT(version) {
		t.Skipf("Only tested in %s and later", version)
	}
}

func IsLessThan(version semver.Version) bool {
	return releaseVersion.LT(version)
}
