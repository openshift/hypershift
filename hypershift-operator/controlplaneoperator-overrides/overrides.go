package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v2"
)

const (
	CPOOverridesEnvVar = "ENABLE_CPO_OVERRIDES"
)

type CPOOverrides struct {
	Platforms CPOPlatforms `yaml:"platforms,omitempty"`
}

type CPOPlatforms struct {
	AWS   *CPOPlatformOverrides `yaml:"aws,omitempty"`
	Azure *CPOPlatformOverrides `yaml:"azure,omitempty"`
}

type CPOPlatformOverrides struct {
	Overrides []CPOOverride            `yaml:"overrides,omitempty"`
	Testing   *CPOOverrideTestReleases `yaml:"testing,omitempty"`
}

type CPOOverride struct {
	Version  string `yaml:"version"`
	CPOImage string `yaml:"cpoImage"`
}

type CPOOverrideTestReleases struct {
	Latest   string `yaml:"latest"`
	Previous string `yaml:"previous"`
	RunTests bool   `yaml:"runTests"`
}

//go:embed assets/overrides.yaml
var overridesYAML []byte

// scopedVersionPattern matches the literal major.minor.patch- prefix a glob
// version must start with. Scoping patterns to one release keeps them from
// matching the plain release or another version by accident.
var scopedVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+-`)

var (
	overrides           = mustLoadOverrides()
	overridesByPlatform map[string][]CPOOverride
)

func init() {
	initOverridesByPlatform()
}

func IsOverridesEnabled() bool {
	return os.Getenv(CPOOverridesEnvVar) == "1"
}

// CPOImage returns the control plane operator image configured in the overrides
// for the given platform and release version, or an empty string when there is
// no override.
//
// The version is the string carried by the release payload, including any
// suffix (for example 4.20.15-hotfix34123420). An override whose version equals
// it exactly applies; if several do, the last entry wins. Otherwise, override
// versions containing glob wildcards are tried in the order they appear in the
// file and the first match applies, matched with path.Match: an entry for
// "4.20.15-*" covers every suffixed build of 4.20.15 without naming the build,
// while an entry for "4.20.15" covers the plain release only. Globs must start
// with a literal major.minor.patch- prefix, and an exact match always wins over
// a glob.
func CPOImage(rawPlatform, version string) string {
	return getCPOImage(rawPlatform, version, overridesByPlatform)
}

func getCPOImage(rawPlatform string, version string, mapOverrides map[string][]CPOOverride) string {
	platform := strings.ToLower(rawPlatform)
	platformOverrides, platformExists := mapOverrides[platform]
	if !platformExists {
		return ""
	}
	// An exact match wins over any glob so that a payload can be pinned to one
	// specific entry (for example 4.20.15-hotfix34123420) no matter where that
	// entry sits relative to the patterns. Iterate backwards so a duplicate entry
	// still supersedes the earlier one, as the previous map-based lookup did.
	for i := len(platformOverrides) - 1; i >= 0; i-- {
		if platformOverrides[i].Version == version {
			return platformOverrides[i].CPOImage
		}
	}
	// Globs are matched in file order, so a more specific pattern must be listed
	// before a more general one.
	for _, override := range platformOverrides {
		if !strings.ContainsAny(override.Version, "*?[") {
			continue
		}
		// Patterns are validated when the overrides are loaded; skip the entry
		// defensively if path.Match still reports a malformed pattern.
		if matched, err := path.Match(override.Version, version); err == nil && matched {
			return override.CPOImage
		}
	}
	return ""
}

func LatestOverrideTestReleases(platform string) (string, string) {
	return overrideTestReleases(platform, overrides)
}

func ShouldRunOverrideTests(platform string) bool {
	return shouldRunOverrideTests(platform, overrides)
}

func overrideTestReleases(platform string, o *CPOOverrides) (string, string) {
	switch strings.ToLower(platform) {
	case "aws":
		if o.Platforms.AWS != nil && o.Platforms.AWS.Testing != nil {
			return o.Platforms.AWS.Testing.Latest, o.Platforms.AWS.Testing.Previous
		}
	case "azure":
		if o.Platforms.Azure != nil && o.Platforms.Azure.Testing != nil {
			return o.Platforms.Azure.Testing.Latest, o.Platforms.Azure.Testing.Previous
		}
	}
	return "", ""
}

func shouldRunOverrideTests(platform string, o *CPOOverrides) bool {
	switch strings.ToLower(platform) {
	case "aws":
		if o.Platforms.AWS != nil && o.Platforms.AWS.Testing != nil {
			return o.Platforms.AWS.Testing.RunTests
		}
	case "azure":
		if o.Platforms.Azure != nil && o.Platforms.Azure.Testing != nil {
			return o.Platforms.Azure.Testing.RunTests
		}
	}
	return false
}

func mustLoadOverrides() *CPOOverrides {
	overrides, err := loadOverrides(overridesYAML)
	if err != nil {
		panic(fmt.Sprintf("Failed to load cpo overrides: %v", err))
	}
	return overrides
}

func loadOverrides(yamlContent []byte) (*CPOOverrides, error) {
	result := &CPOOverrides{}
	if err := yaml.Unmarshal(yamlContent, result); err != nil {
		return nil, err
	}
	for _, platform := range []*CPOPlatformOverrides{result.Platforms.AWS, result.Platforms.Azure} {
		if platform == nil {
			continue
		}
		for _, override := range platform.Overrides {
			if strings.ContainsAny(override.Version, "*?[") && !scopedVersionPattern.MatchString(override.Version) {
				return nil, fmt.Errorf("invalid version pattern %q: glob patterns must start with a major.minor.patch- prefix, for example 4.20.15-*", override.Version)
			}
			// Version entries may be glob patterns; catch malformed ones when the
			// overrides are loaded instead of silently ignoring them at match time.
			if _, err := path.Match(override.Version, ""); err != nil {
				return nil, fmt.Errorf("invalid version pattern %q: %w", override.Version, err)
			}
		}
	}
	return result, nil
}

func initOverridesByPlatform() {
	overridesByPlatform = getOverridesByPlatform(overrides)
}

func getOverridesByPlatform(o *CPOOverrides) map[string][]CPOOverride {
	result := map[string][]CPOOverride{}
	if o.Platforms.AWS != nil {
		result["aws"] = o.Platforms.AWS.Overrides
	}
	if o.Platforms.Azure != nil {
		result["azure"] = o.Platforms.Azure.Overrides
	}
	return result
}
