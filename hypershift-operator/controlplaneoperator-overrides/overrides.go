package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/openshift/hypershift/support/config"
	"gopkg.in/yaml.v2"
)

type CPOOverrides struct {
	Overrides []CPOOverride `yaml:"overrides,omitempty"`
}

type CPOOverride struct {
	Version  string                  `yaml:"version"`
	CPOImage string                  `yaml:"cpoImage"`
	Testing  CPOOverrideTestReleases `yaml:"testing"`
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
	return lookupCPOImage(version)
}

func LatestOverrideTestReleases() (string, string) {
	if len(overrides.Overrides) > 0 {
		testingReleases := overrides.Overrides[len(overrides.Overrides)-1].Testing
		return testingReleases.Latest, testingReleases.Previous
	}
	return "", ""
}

func lookupCPOImage(version string) string {
	fmt.Println("overridesByVersion", overridesByVersion)
	if override, exists := overridesByVersion[version]; exists {
		return override.CPOImage
	}
	return ""
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
