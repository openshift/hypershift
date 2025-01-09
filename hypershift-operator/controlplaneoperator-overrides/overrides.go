package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/openshift/hypershift/support/config"

	"gopkg.in/yaml.v2"
)

type CPOOverrides struct {
	Overrides []CPOOverride           `yaml:"overrides,omitempty"`
	Testing   CPOOverrideTestReleases `yaml:"testing"`
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
	overrides          = mustLoadOverrides()
	overridesByVersion map[string]*CPOOverride
)

func init() {
	initOverridesByVersion()
}

func IsOverridesEnabled() bool {
	return os.Getenv(config.CPOOverridesEnvVar) == "1"
}

func CPOImage(version string) string {
	if override, exists := overridesByVersion[version]; exists {
		return override.CPOImage
	}
	return ""
}

func LatestOverrideTestReleases() (string, string) {
	return overrides.Testing.Latest, overrides.Testing.Previous
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

func initOverridesByVersion() {
	overridesByVersion = map[string]*CPOOverride{}
	for i, override := range overrides.Overrides {
		overridesByVersion[override.Version] = &overrides.Overrides[i]
	}
}
