package supportedversion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"

	"github.com/blang/semver"
)

// LatestSupportedVersion is the latest minor OCP version supported by the
// HyperShift operator.
// NOTE: The .0 (z release) should be ignored. It's only here to support
// semver parsing.
var LatestSupportedVersion = semver.MustParse("4.20.0")
var MinSupportedVersion = semver.MustParse("4.14.0")

func GetMinSupportedVersion(hc *hyperv1.HostedCluster) semver.Version {

	if _, exists := hc.Annotations[hyperv1.SkipReleaseImageValidation]; exists {
		return semver.MustParse("0.0.0")
	}

	defaultMinVersion := MinSupportedVersion
	switch hc.Spec.Platform.Type {
	case hyperv1.IBMCloudPlatform:
		return semver.MustParse("4.9.0")
	default:
		return defaultMinVersion
	}
}

func Supported() []string {
	versions := []string{trimVersion(LatestSupportedVersion.String())}
	for i := 0; i < int(LatestSupportedVersion.Minor-MinSupportedVersion.Minor); i++ {
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

func IsValidReleaseVersion(version, currentVersion, maxSupportedVersion, minSupportedVersion *semver.Version, networkType hyperv1.NetworkType, platformType hyperv1.PlatformType) error {
	if maxSupportedVersion.GT(LatestSupportedVersion) {
		maxSupportedVersion = ptr.To(LatestSupportedVersion)
	}

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

	if (version.Major == maxSupportedVersion.Major && version.Minor > maxSupportedVersion.Minor) || version.Major > maxSupportedVersion.Major {
		return fmt.Errorf("the latest version supported is: %q. Attempting to use: %q", maxSupportedVersion, version)
	}

	if (version.Major == minSupportedVersion.Major && version.Minor < minSupportedVersion.Minor) || version.Major < minSupportedVersion.Major {
		return fmt.Errorf("the minimum version supported for platform %s is: %q. Attempting to use: %q", string(platformType), minSupportedVersion, version)
	}

	return nil
}

type ocpVersion struct {
	Name        string `json:"name"`
	PullSpec    string `json:"pullSpec"`
	DownloadURL string `json:"downloadURL"`
}

// LookupLatestSupportedRelease picks the latest multi-arch image supported by this Hypershift Operator
func LookupLatestSupportedRelease(ctx context.Context, hc *hyperv1.HostedCluster) (string, error) {

	minSupportedVersion := GetMinSupportedVersion(hc)

	prefix := "https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable-multi/latest"
	filter := fmt.Sprintf("in=>4.%d.%d+<+4.%d.0-a",
		minSupportedVersion.Minor, minSupportedVersion.Patch, LatestSupportedVersion.Minor+1)

	releaseURL := fmt.Sprintf("%s?%s", prefix, filter)

	var version ocpVersion

	req, err := http.NewRequestWithContext(ctx, "GET", releaseURL, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(body, &version)
	if err != nil {
		return "", err
	}
	return version.PullSpec, nil
}
