package supportedversion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sort"
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
	releaseURLTemplate = "https://%s.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags"
)

// LatestSupportedVersion is the latest minor OCP version supported by the
// HyperShift operator.
// NOTE: The .0 (z release) should be ignored. It's only here to support
// semver parsing.
var (
	LatestSupportedVersion      = semver.MustParse("4.22.0")
	MinSupportedVersion         = semver.MustParse("4.14.0")
	IBMCloudMinSupportedVersion = semver.MustParse("4.14.0")
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
	"4.21.0": semver.MustParse("1.34.0"),
}

func GetKubeVersionForSupportedVersion(supportedVersion semver.Version) (*semver.Version, error) {
	kubeVersion, ok := ocpVersionToKubeVersion[supportedVersion.String()]
	if !ok {
		return nil, fmt.Errorf("unknown supported version %q", supportedVersion.String())
	}

	return &kubeVersion, nil
}

// Get the minimum OCP version for HostedClusters, not the management cluster where HO runs.
func GetMinSupportedVersion(hc *hyperv1.HostedCluster) semver.Version {
	if _, exists := hc.Annotations[hyperv1.SkipReleaseImageValidation]; exists {
		return semver.MustParse("0.0.0")
	}

	defaultMinVersion := MinSupportedVersion
	switch hc.Spec.Platform.Type {
	// Red Hat OpenShift on IBM Cloud (ROKS) may support OCP versions beyond
	// standard OCP version support timelines (see [1]). Please contact ROKS
	// development before changing values here.
	// [1] https://cloud.ibm.com/docs/openshift?topic=openshift-openshift_versions
	case hyperv1.IBMCloudPlatform:
		return IBMCloudMinSupportedVersion
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

// LookupDefaultOCPVersion retrieves the default OCP version from release streams.
// It supports two modes of operation:
//
//  1. When releaseStream is empty: Uses the default release stream (4-stable-multi) and looks up supported OCP versions
//     from the HyperShift operator's ConfigMap to find the latest supported version that is not a release candidate.
//
//  2. When releaseStream is provided: Uses the specified release stream. The function detects the architecture from
//     the stream suffix (e.g., "-multi", "-arm64", "-ppc64le", "-s390x") and routes to the appropriate endpoint.
//     Streams without a recognized suffix default to the amd64 endpoint.
//
// All streams use the /tags endpoint which returns a list of versions, enabling proper RC filtering and version
// validation against the supported-versions ConfigMap.
func LookupDefaultOCPVersion(ctx context.Context, releaseStream string, client crclient.Client) (ocpVersion, error) {
	if len(releaseStream) == 0 {
		releaseStream = config.DefaultReleaseStream
	}

	arch := getArchFromStream(releaseStream)
	releaseURL := fmt.Sprintf(releaseURLTemplate, arch, releaseStream)

	version, err := retrieveSupportedOCPVersion(ctx, releaseURL, client)
	if err != nil {
		return ocpVersion{}, fmt.Errorf("failed to get OCP version from release URL %s: %v", releaseURL, err)
	}

	return version, nil
}

// getArchFromStream returns the architecture identifier for the release stream endpoint.
// It detects the architecture from the stream suffix:
//   - "-multi" -> "multi" (multi-arch images)
//   - "-arm64" -> "arm64" (aarch64 images)
//   - "-ppc64le" -> "ppc64le" (IBM Power images)
//   - "-s390x" -> "s390x" (IBM Z images)
//   - default -> "amd64" (x86_64 images)
func getArchFromStream(releaseStream string) string {
	switch {
	case strings.HasSuffix(releaseStream, "-multi"):
		return "multi"
	case strings.HasSuffix(releaseStream, "-arm64"):
		return "arm64"
	case strings.HasSuffix(releaseStream, "-ppc64le"):
		return "ppc64le"
	case strings.HasSuffix(releaseStream, "-s390x"):
		return "s390x"
	default:
		return "amd64"
	}
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

// commitHash is set via -ldflags at build time as a fallback when
// Go's VCS stamping is unavailable (e.g. git worktrees, vendored builds).
var commitHash string

// GetRevision returns the overall codebase version. It's for detecting
// what code a binary was built from.
func GetRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range bi.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}

	if commitHash != "" {
		return commitHash
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
		supportedVersions = manifests.ConfigMap(namespace)
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

// ValidateVersionSkew validates the version skew between HostedCluster and NodePool versions.
// Returns nil if the version skew is supported, otherwise returns a descriptive error.
// All 4.y versions support n-3 version skew (e.g., 4.18 HostedCluster supports NodePools running 4.17, 4.16, and 4.15).
func ValidateVersionSkew(hostedClusterVersion, nodePoolVersion *semver.Version) error {
	// Reject mismatched major versions
	if nodePoolVersion.Major != hostedClusterVersion.Major {
		return fmt.Errorf("NodePool major version %d must match HostedCluster major version %d",
			nodePoolVersion.Major, hostedClusterVersion.Major)
	}

	if nodePoolVersion.GT(*hostedClusterVersion) {
		return fmt.Errorf("NodePool version %s cannot be higher than the HostedCluster version %s",
			nodePoolVersion, hostedClusterVersion)
	}

	versionDiff := int(hostedClusterVersion.Minor) - int(nodePoolVersion.Minor)
	maxAllowedDiff := 3

	if versionDiff > maxAllowedDiff {
		// Compute minSupportedMinor with explicit conditional
		var minSupportedMinor int
		if int(hostedClusterVersion.Minor)-maxAllowedDiff < 0 {
			minSupportedMinor = 0
		} else {
			minSupportedMinor = int(hostedClusterVersion.Minor) - maxAllowedDiff
		}
		return fmt.Errorf("NodePool minor version %d.%d is less than %d.%d, which is the minimum NodePool version compatible with the %d.%d HostedCluster",
			nodePoolVersion.Major, nodePoolVersion.Minor,
			hostedClusterVersion.Major, minSupportedMinor,
			hostedClusterVersion.Major, hostedClusterVersion.Minor)
	}

	return nil
}

// retrieveSupportedOCPVersion retrieves the latest supported OCP version from the supported-versions ConfigMap,
// fetches release information from the provided release URL, and returns the latest supported OCP version that is
// not a release candidate and is compatible with the currently installed HyperShift operator.
//
// The releaseURL should point to a /tags endpoint which returns a list of all available versions for the stream.
// The function filters out release candidates and validates versions against the supported-versions ConfigMap,
// returning the newest supported non-RC version.
func retrieveSupportedOCPVersion(ctx context.Context, releaseURL string, client crclient.Client) (ocpVersion, error) {
	var supportedVersions *corev1.ConfigMap
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

	// Fetch the release information from the URL
	resp, err := http.Get(releaseURL)
	if err != nil {
		return ocpVersion{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ocpVersion{}, err
	}

	// Try to parse as tags response first (multi-arch /tags endpoint)
	var stableOCPVersions ocpTags
	if err := json.Unmarshal(body, &stableOCPVersions); err == nil && len(stableOCPVersions.Tags) > 0 {
		// Successfully parsed as tags - find latest supported non-RC version
		return findLatestSupportedVersion(supportedOCPVersions.Versions, stableOCPVersions.Tags, releaseURL)
	}

	// Try to parse as single version response (amd64 /latest endpoint)
	var singleVersion ocpVersion
	if err := json.Unmarshal(body, &singleVersion); err == nil && singleVersion.Name != "" {
		// Got a single version - check if it's supported and not RC
		if strings.Contains(singleVersion.Name, "rc") {
			return ocpVersion{}, fmt.Errorf("the latest version in stream is a release candidate (%s), which is not supported by this HyperShift Operator", singleVersion.Name)
		}

		// Check if this version is in our supported list
		for _, supportedVer := range supportedOCPVersions.Versions {
			if strings.Contains(singleVersion.Name, supportedVer) {
				return singleVersion, nil
			}
		}

		return ocpVersion{}, fmt.Errorf("version %s from release stream is not supported by this HyperShift Operator (supported: %v)",
			singleVersion.Name, supportedOCPVersions.Versions)
	}

	return ocpVersion{}, fmt.Errorf("failed to parse response from release URL %s", releaseURL)
}

// findLatestSupportedVersion finds the latest version from a list of tags that is both supported by the
// HyperShift operator and not a release candidate. It filters out RC versions, sorts the remaining tags
// by version (newest first), and returns the first tag that matches a supported version.
func findLatestSupportedVersion(supportedVersions []string, tags []ocpVersion, releaseURL string) (ocpVersion, error) {
	// Filter out release candidates
	var nonRCTags []ocpVersion
	for _, tag := range tags {
		if !strings.Contains(tag.Name, "rc") {
			nonRCTags = append(nonRCTags, tag)
		}
	}

	// Sort non-RC tags by version in descending order (newest first)
	sort.Slice(nonRCTags, func(i, j int) bool {
		semverI, errI := semver.Parse(nonRCTags[i].Name)
		semverJ, errJ := semver.Parse(nonRCTags[j].Name)

		// If parsing fails, fall back to string comparison
		if errI != nil || errJ != nil {
			return nonRCTags[i].Name > nonRCTags[j].Name
		}

		// Compare semver (GT returns true if i > j, which gives us descending order)
		return semverI.GT(semverJ)
	})

	// Find the first tag that matches a supported version
	for _, tag := range nonRCTags {
		for _, version := range supportedVersions {
			if strings.Contains(tag.Name, version) {
				return tag, nil
			}
		}
	}

	return ocpVersion{}, fmt.Errorf("failed to find the latest supported OCP version in the release stream %s", releaseURL)
}
