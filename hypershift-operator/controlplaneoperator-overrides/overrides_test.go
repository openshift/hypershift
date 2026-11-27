package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

func TestCPOImage(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		version  string
		expected string
	}{
		{
			name:     "When the platform has different casing, it should match case-insensitively",
			platform: "AWS",
			version:  "4.17.8",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.8",
		},
		{
			name:     "When an exact version override exists, it should return that image",
			platform: "aws",
			version:  "4.17.9",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.9",
		},
		{
			name:     "When an azure override exists, it should return the azure image",
			platform: "Azure",
			version:  "4.17.9",
			expected: "quay.io/hypershift/control-plane-operator-azure:4.17.9",
		},
		{
			name:     "When no override exists for the version, it should return an empty image",
			platform: "aws",
			version:  "4.13.9",
			expected: "",
		},
		{
			name:     "When the platform has no overrides, it should return an empty image",
			platform: "foo",
			version:  "4.17.9",
			expected: "",
		},
		{
			name:     "When the version carries a hotfix suffix and a matching glob exists, it should return the glob image",
			platform: "aws",
			version:  "4.17.8-hotfix99999999",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.8-suffixed",
		},
		{
			name:     "When the version carries build metadata, it should not match a dash glob",
			platform: "aws",
			version:  "4.17.8+build.1234",
			expected: "",
		},
		{
			name:     "When the version is the plain release and only a suffixed glob exists, it should return an empty image",
			platform: "aws",
			version:  "4.18.2",
			expected: "",
		},
		{
			name:     "When the version carries a suffix and only a suffixed glob exists, it should return the glob image",
			platform: "aws",
			version:  "4.18.2-hotfix34123420",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.18.2-suffixed",
		},
		{
			name:     "When an override exists for the exact suffixed version, it should prefer it over the glob",
			platform: "aws",
			version:  "4.17.8-hotfix34123420",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.8-hotfix34123420",
		},
		{
			name:     "When only a plain version override exists, it should not apply to a suffixed version",
			platform: "azure",
			version:  "4.16.5-hotfix34123420",
			expected: "",
		},
		{
			name:     "When several globs match, it should return the first one in file order",
			platform: "aws",
			version:  "4.19.0-hotfix34123420",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.19.0-first",
		},
		{
			name:     "When the suffixed version has no matching override, it should return an empty image",
			platform: "aws",
			version:  "4.13.9-hotfix34123420",
			expected: "",
		},
		{
			name:     "When the version is a prefix of an overridden version, it should not match",
			platform: "aws",
			version:  "4.17",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			actual := getCPOImage(test.platform, test.version, getOverridesByPlatform(fakeOverrides()))
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestLatestOverrideTestReleases(t *testing.T) {
	g := NewWithT(t)
	resultLatest, resultPrevious := overrideTestReleases("aws", fakeOverrides())
	g.Expect(resultLatest).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"))
	g.Expect(resultPrevious).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"))
}

func TestLoadOverridesSample(t *testing.T) {
	g := NewWithT(t)
	o, err := loadOverrides(overridesSampleYAML)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(o).ToNot(BeNil())
	g.Expect(o.Platforms.AWS.Testing.Previous).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"))
	g.Expect(o.Platforms.AWS.Testing.Latest).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"))

	g.Expect(o.Platforms.AWS.Overrides).To(HaveLen(2))
	g.Expect(o.Platforms.AWS.Overrides[0].Version).To(Equal("4.17.9"))
	g.Expect(o.Platforms.AWS.Overrides[0].CPOImage).To(Equal("quay.io/hypershift/control-plane-operator:4.17.9"))
	g.Expect(o.Platforms.AWS.Overrides[1].Version).To(Equal("4.17.8"))
	g.Expect(o.Platforms.AWS.Overrides[1].CPOImage).To(Equal("quay.io/hypershift/control-plane-operator:4.17.8"))
}

func TestLoadOverrides(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent []byte
		expectedErr string
	}{
		{
			name:        "When the embedded overrides are loaded, it should return the configured overrides",
			yamlContent: overridesYAML,
		},
		{
			name:        "When an override uses a glob pattern, it should load it",
			yamlContent: []byte("platforms:\n  aws:\n    overrides:\n      - version: \"4.20.15-*\"\n        cpoImage: \"quay.io/hypershift/control-plane-operator-aws:4.20.15\"\n"),
		},
		{
			name:        "When an override has a malformed glob pattern, it should return an error",
			yamlContent: []byte("platforms:\n  aws:\n    overrides:\n      - version: \"4.20.15-[\"\n        cpoImage: \"quay.io/hypershift/control-plane-operator-aws:4.20.15\"\n"),
			expectedErr: "invalid version pattern",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			o, err := loadOverrides(test.yamlContent)
			if test.expectedErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(test.expectedErr)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(o).ToNot(BeNil())
			g.Expect(o.Platforms.AWS.Overrides).ToNot(BeEmpty())
		})
	}
}

func TestExistingOverride(t *testing.T) {
	g := NewWithT(t)
	result := CPOImage("aws", "4.15.53")
	g.Expect(result).To(Equal("quay.io/redhat-user-workloads/crt-redhat-acm-tenant/openshift-cert-hotfix-415-multi:ab45f5df65f19c9e4db4977b0d756c8d87baff0a"))
}

func fakeOverrides() *CPOOverrides {
	result := &CPOOverrides{
		Platforms: CPOPlatforms{
			AWS: &CPOPlatformOverrides{
				Testing: &CPOOverrideTestReleases{
					Latest:   "quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64",
					Previous: "quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64",
				},
			},
			Azure: &CPOPlatformOverrides{
				Testing: &CPOOverrideTestReleases{
					Latest:   "quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64",
					Previous: "quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64",
				},
			},
		},
	}
	generateOverrides := func(platform string) []CPOOverride {
		result := []CPOOverride{}
		for _, minor := range []int{15, 16, 17} {
			for patch := range 10 {
				result = append(result, CPOOverride{
					Version:  fmt.Sprintf("4.%d.%d", minor, patch),
					CPOImage: fmt.Sprintf("quay.io/hypershift/control-plane-operator-%s:4.%d.%d", platform, minor, patch),
				})
			}
		}
		return result
	}
	result.Platforms.AWS.Overrides = generateOverrides("aws")
	// Glob entries cover every suffixed build of a z-stream without naming the
	// build; the exact suffixed entry pins one specific build.
	result.Platforms.AWS.Overrides = append(result.Platforms.AWS.Overrides,
		CPOOverride{
			Version:  "4.17.8-*",
			CPOImage: "quay.io/hypershift/control-plane-operator-aws:4.17.8-suffixed",
		},
		CPOOverride{
			Version:  "4.17.8-hotfix34123420",
			CPOImage: "quay.io/hypershift/control-plane-operator-aws:4.17.8-hotfix34123420",
		},
		CPOOverride{
			Version:  "4.18.2-*",
			CPOImage: "quay.io/hypershift/control-plane-operator-aws:4.18.2-suffixed",
		},
		CPOOverride{
			Version:  "4.19.0-*",
			CPOImage: "quay.io/hypershift/control-plane-operator-aws:4.19.0-first",
		},
		CPOOverride{
			Version:  "4.19.*",
			CPOImage: "quay.io/hypershift/control-plane-operator-aws:4.19.0-second",
		},
	)
	result.Platforms.Azure.Overrides = generateOverrides("azure")
	return result
}

//go:embed assets/overrides_sample.yaml
var overridesSampleYAML []byte
