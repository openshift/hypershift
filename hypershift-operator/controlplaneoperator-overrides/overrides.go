package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"os"
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

var (
	overrides                     = mustLoadOverrides()
	overridesByPlatformAndVersion map[string]map[string]*CPOOverride
)

func init() {
	initOverridesByPlatformAndVersion()
}

func IsOverridesEnabled() bool {
	return os.Getenv(CPOOverridesEnvVar) == "1"
}

func CPOImage(rawPlatform, version string) string {
	return getCPOImage(rawPlatform, version, overridesByPlatformAndVersion)
}

func getCPOImage(rawPlatform string, version string, mapOverrides map[string]map[string]*CPOOverride) string {
	platform := strings.ToLower(rawPlatform)
	platformOverrides, platformExists := mapOverrides[platform]
	if !platformExists {
		return ""
	}
	if override, exists := platformOverrides[version]; exists {
		return override.CPOImage
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
	p, ok := o.activePlatforms()[strings.ToLower(platform)]
	if !ok || p.Testing == nil {
		return "", ""
	}
	return p.Testing.Latest, p.Testing.Previous
}

func shouldRunOverrideTests(platform string, o *CPOOverrides) bool {
	p, ok := o.activePlatforms()[strings.ToLower(platform)]
	if !ok || p.Testing == nil {
		return false
	}
	return p.Testing.RunTests
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
	return result, nil
}

func initOverridesByPlatformAndVersion() {
	overridesByPlatformAndVersion = getOverridesByPlatformAndVersion(overrides)
}

func getOverridesByPlatformAndVersion(o *CPOOverrides) map[string]map[string]*CPOOverride {
	result := map[string]map[string]*CPOOverride{}
	for name, p := range o.activePlatforms() {
		result[name] = map[string]*CPOOverride{}
		for i, override := range p.Overrides {
			result[name][override.Version] = &p.Overrides[i]
		}
	}
	return result
}

// AllOverrideImages returns all unique CPO override images along with the
// platform/version entries that reference each image. This enables validation
// of override images without inspecting duplicates, since many version entries
// share the same image digest.
func AllOverrideImages() map[string][]string {
	return allOverrideImages(overrides)
}

func allOverrideImages(o *CPOOverrides) map[string][]string {
	result := make(map[string][]string)
	for name, p := range o.activePlatforms() {
		for _, override := range p.Overrides {
			if override.CPOImage == "" {
				continue
			}
			result[override.CPOImage] = append(result[override.CPOImage], fmt.Sprintf("%s/%s", name, override.Version))
		}
	}
	return result
}

// activePlatforms returns a map of platform name to platform overrides for all
// non-nil platforms. This centralizes platform enumeration to reduce the risk
// of missing a platform when new ones are added.
func (o *CPOOverrides) activePlatforms() map[string]*CPOPlatformOverrides {
	platforms := make(map[string]*CPOPlatformOverrides)
	if o.Platforms.AWS != nil {
		platforms["aws"] = o.Platforms.AWS
	}
	if o.Platforms.Azure != nil {
		platforms["azure"] = o.Platforms.Azure
	}
	return platforms
}
