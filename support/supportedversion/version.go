package supportedversion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	manifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/supportedversion"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

// https://docs.ci.openshift.org/docs/getting-started/useful-links/#services
const (
	multiArchReleaseURLTemplate = "https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags"
	releaseURLTemplate          = "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/latest"
)

// LatestSupportedVersion is the latest minor OCP version supported by the
// HyperShift operator.
// NOTE: The .0 (z release) should be ignored. It's only here to support
// semver parsing.
var (
	LatestSupportedVersion = semver.MustParse("4.20.0")
	MinSupportedVersion    = semver.MustParse("4.14.0")
)

// ocpVersionToKubeVersion maps OCP versions to their corresponding Kubernetes versions.
// This mapping is used to determine the Kubernetes version for a given OCP version.
var ocpVersionToKubeVersion = map[string]semver.Version{
	"4.14.0": semver.MustParse("1.27.0"),
	"4.15.0": semver.MustParse("1.28.0"),
	"4.16.0": semver.MustParse("1.29.0"),
	"4.17.0": semver.MustParse("1.30.0"),
	"4.18.0": semver.MustParse("1.31.0"),
	"4.19.0": semver.MustParse("1.32.0"),
	"4.20.0": semver.MustParse("1.33.0"),
}

func GetKubeVersionForSupportedVersion(supportedVersion semver.Version) (*semver.Version, error) {
	kubeVersion, ok := ocpVersionToKubeVersion[supportedVersion.String()]
	if !ok {
		return nil, fmt.Errorf("unknown supported version %q", supportedVersion.String())
	}

	return &kubeVersion, nil
}

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

func getOCPVersion(releaseURL string) (ocpVersion, error) {
	var version ocpVersion

	resp, err := http.Get(releaseURL)
	if err != nil {
		return version, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return version, err
	}
	err = json.Unmarshal(body, &version)
	if err != nil {
		return version, err
	}
	return version, nil
}

func LookupDefaultOCPVersion(ctx context.Context, releaseStream string, client crclient.Client) (ocpVersion, error) {
	var (
		version    ocpVersion
		err        error
		releaseURL string
	)

	if len(releaseStream) == 0 {
		// No release stream was provided, so we will look up the supported OCP versions from the HO and use the latest
		// release image from the multi-arch release stream that is not a release candidate.
		releaseURL = fmt.Sprintf(multiArchReleaseURLTemplate, config.DefaultReleaseStream)
		version, err = retrieveSupportedOCPVersion(ctx, releaseURL, client)
	} else {
		// We look up the release URL based on the user provided release stream.
		releaseURL = fmt.Sprintf(releaseURLTemplate, releaseStream)
		version, err = getOCPVersion(releaseURL)
	}

	if err != nil {
		return ocpVersion{}, fmt.Errorf("failed to get OCP version from release URL %s: %v", releaseURL, err)
	}

	return version, nil
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

// GetRevision returns the overall codebase version. It's for detecting
// what code a binary was built from.
func GetRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "<unknown>"
	}

	for _, setting := range bi.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return "<unknown>"
}

func String() string {
	return fmt.Sprintf("openshift/hypershift: %s. Latest supported OCP: %s", GetRevision(), LatestSupportedVersion)
}

type SupportedVersions struct {
	Versions []string `json:"versions"`
}

// GetSupportedOCPVersions retrieves the supported OCP versions from the server. It fetches the ConfigMap containing the
// supported versions and the server version from the specified namespace and unmarshals the versions into a
// SupportedVersions struct. If the ConfigMap or the required keys are not found, it returns an error. The function
// returns the supported versions, the server version, and any error encountered during the process.
func GetSupportedOCPVersions(ctx context.Context, namespace string, client crclient.Client, supportedVersions *corev1.ConfigMap) (SupportedVersions, string, error) {
	var versions SupportedVersions
	var serverVersion string

	if supportedVersions == nil {
		// Fetch the supported versions ConfigMap from the specified namespace
		supportedVersions := manifests.ConfigMap(namespace)
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(supportedVersions), supportedVersions); err != nil {
			return SupportedVersions{}, "", fmt.Errorf("failed to find supported versions on the server: %v", err)
		}
	}

	// Check if the ConfigMap contains the server version key
	if serverVersionValue, present := supportedVersions.Data[config.ConfigMapServerVersionKey]; present {
		serverVersion = serverVersionValue
	} else {
		return SupportedVersions{}, "", fmt.Errorf("the server did not advertise its HyperShift version")
	}

	// Check if the ConfigMap contains the supported versions key
	if supportedVersionData, present := supportedVersions.Data[config.ConfigMapVersionsKey]; present {
		if err := json.Unmarshal([]byte(supportedVersionData), &versions); err != nil {
			return SupportedVersions{}, "", fmt.Errorf("failed to parse supported versions on the server: %v", err)
		}

		return versions, serverVersion, nil
	} else {
		return SupportedVersions{}, "", fmt.Errorf("the server did not advertise supported OCP versions")
	}
}

type ocpTags struct {
	Name string       `json:"name"`
	Tags []ocpVersion `json:"tags"`
}

// retrieveSupportedOCPVersion retrieves the latest supported OCP version from supported versions ConfigMap, retrieves
// the latest stable release images from the provided release URL, and returns the latest supported OCP version that is
// not a release candidate and matches the latest supported OCP version supported by the HyperShift operator.
func retrieveSupportedOCPVersion(ctx context.Context, releaseURL string, client crclient.Client) (ocpVersion, error) {
	var supportedVersions *corev1.ConfigMap
	var stableOCPVersions ocpTags
	var namespace string

	// Find the supported versions ConfigMap since it may be in a different namespace than the default "hypershift"
	configMapList := &corev1.ConfigMapList{}
	err := client.List(ctx, configMapList, crclient.MatchingLabels{"hypershift.openshift.io/supported-versions": "true"})
	if err != nil {
		return ocpVersion{}, fmt.Errorf("failed to list ConfigMaps to find supported versions: %v", err)
	}
	for _, configMap := range configMapList.Items {
		if configMap.Name == "supported-versions" {
			supportedVersions = &configMap
			namespace = configMap.Namespace
			break
		}
	}

	if namespace == "" {
		return ocpVersion{}, fmt.Errorf("failed to find supported versions ConfigMap")
	}

	// Get the latest supported OCP version from the supported versions ConfigMap
	supportedOCPVersions, _, err := GetSupportedOCPVersions(ctx, namespace, client, supportedVersions)
	if err != nil {
		return ocpVersion{}, fmt.Errorf("failed to get supported OCP versions: %v", err)
	}
	if len(supportedOCPVersions.Versions) == 0 {
		return ocpVersion{}, fmt.Errorf("no supported OCP versions found in the ConfigMap")
	}

	// Grab the latest stable release images
	resp, err := http.Get(releaseURL)
	if err != nil {
		return ocpVersion{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ocpVersion{}, err
	}
	err = json.Unmarshal(body, &stableOCPVersions)
	if err != nil {
		return ocpVersion{}, err
	}

	// Find the latest supported OCP version that is not a release candidate and matches the latest supported OCP
	// version supported by the HyperShift operator.
	for _, version := range supportedOCPVersions.Versions {
		for _, ocpVersion := range stableOCPVersions.Tags {
			if strings.Contains(ocpVersion.Name, "rc") {
				// Skip release candidates
				continue
			}
			if strings.Contains(ocpVersion.Name, version) {
				// We found the latest supported OCP version
				return ocpVersion, nil
			}
		}
	}

	return ocpVersion{}, fmt.Errorf("failed to find the latest supported OCP version in the release stream %s", releaseURL)
}
