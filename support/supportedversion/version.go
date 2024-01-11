package supportedversion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/blang/semver"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/supportedversion/supported"
)

func GetMinSupportedVersion(hc *hyperv1.HostedCluster) semver.Version {

	if _, exists := hc.Annotations[hyperv1.SkipReleaseImageValidation]; exists {
		return semver.MustParse("0.0.0")
	}

	defaultMinVersion := semver.MustParse(supported.SubtractMinor(&supported.LatestSupportedVersion, uint64(supported.SupportedPreviousMinorVersions)).String())

	switch hc.Spec.Platform.Type {
	case hyperv1.IBMCloudPlatform:
		return semver.MustParse("4.9.0")
	default:
		return defaultMinVersion
	}
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
		return fmt.Errorf("the latest version supported is: %q. Attempting to use: %q", supported.LatestSupportedVersion, version)
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
	filter := fmt.Sprintf("in=>4.%d.%d+<+4.%d.0",
		minSupportedVersion.Minor, minSupportedVersion.Patch, supported.LatestSupportedVersion.Minor+1)

	releaseURL := fmt.Sprintf("%s?%s", prefix, filter)

	var version ocpVersion

	req, err := http.NewRequestWithContext(ctx, "GET", releaseURL, nil)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Unexpected status code: %d", resp.StatusCode)
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
