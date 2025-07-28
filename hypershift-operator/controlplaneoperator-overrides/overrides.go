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
	if o.Platforms.AWS != nil {
		result["aws"] = map[string]*CPOOverride{}
		for i, override := range o.Platforms.AWS.Overrides {
			result["aws"][override.Version] = &o.Platforms.AWS.Overrides[i]
		}
	}
	if o.Platforms.Azure != nil {
		result["azure"] = map[string]*CPOOverride{}
		for i, override := range o.Platforms.Azure.Overrides {
			result["azure"][override.Version] = &o.Platforms.Azure.Overrides[i]
		}
	}
	return result
}
