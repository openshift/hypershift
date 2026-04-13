package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

//go:embed assets/overrides_sample.yaml
var overridesSampleYAML []byte

func TestCPOImage(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		version  string
		expected string
	}{
		{
			name:     "When platform is uppercase AWS with valid version, it should return the correct image",
			platform: "AWS",
			version:  "4.17.8",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.8",
		},
		{
			name:     "When platform is lowercase aws with valid version, it should return the correct image",
			platform: "aws",
			version:  "4.17.9",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.9",
		},
		{
			name:     "When platform is Azure with valid version, it should return the correct image",
			platform: "Azure",
			version:  "4.17.9",
			expected: "quay.io/hypershift/control-plane-operator-azure:4.17.9",
		},
		{
			name:     "When version is not in overrides, it should return empty string",
			platform: "aws",
			version:  "4.13.9",
			expected: "",
		},
		{
			name:     "When platform is unknown, it should return empty string",
			platform: "foo",
			version:  "4.17.9",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			actual := getCPOImage(test.platform, test.version, getOverridesByPlatformAndVersion(fakeOverrides()))
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestLatestOverrideTestReleases(t *testing.T) {
	t.Run("When platform is aws, it should return the latest and previous releases", func(t *testing.T) {
		g := NewWithT(t)
		resultLatest, resultPrevious := overrideTestReleases("aws", fakeOverrides())
		g.Expect(resultLatest).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"))
		g.Expect(resultPrevious).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"))
	})

	t.Run("When platform is unknown, it should return empty strings", func(t *testing.T) {
		g := NewWithT(t)
		resultLatest, resultPrevious := overrideTestReleases("unknown", fakeOverrides())
		g.Expect(resultLatest).To(BeEmpty())
		g.Expect(resultPrevious).To(BeEmpty())
	})
}

func TestLoadOverridesSample(t *testing.T) {
	t.Run("When loading sample YAML, it should parse all fields correctly", func(t *testing.T) {
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
	})

	t.Run("When YAML is malformed, it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := loadOverrides([]byte("not: valid: yaml: ["))
		g.Expect(err).To(HaveOccurred())
	})
}

func TestLoadOverrides(t *testing.T) {
	t.Run("When loading production overrides, it should parse without error and contain expected platforms", func(t *testing.T) {
		g := NewWithT(t)
		o, err := loadOverrides(overridesYAML)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(o).ToNot(BeNil())
		g.Expect(o.Platforms.AWS.Overrides).ToNot(BeEmpty())
		g.Expect(o.Platforms.Azure.Overrides).ToNot(BeEmpty())
	})
}

func TestExistingOverride(t *testing.T) {
	t.Run("When querying a known production override, it should return the expected image", func(t *testing.T) {
		g := NewWithT(t)
		result := CPOImage("aws", "4.15.53")
		g.Expect(result).To(Equal("quay.io/redhat-user-workloads/crt-redhat-acm-tenant/openshift-cert-hotfix-415-multi:ab45f5df65f19c9e4db4977b0d756c8d87baff0a"))
	})
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
	result.Platforms.Azure.Overrides = generateOverrides("azure")
	return result
}

func TestAllOverrideImages(t *testing.T) {
	t.Run("When using fake overrides, it should return unique images grouped by platform/version", func(t *testing.T) {
		g := NewWithT(t)
		images := allOverrideImages(fakeOverrides())
		g.Expect(images).ToNot(BeEmpty())

		// Each platform has 30 entries (3 minors x 10 patches) and each entry has a unique image
		totalRefs := 0
		for _, refs := range images {
			totalRefs += len(refs)
		}
		g.Expect(totalRefs).To(Equal(60), "Expected 60 total references (30 AWS + 30 Azure)")

		// Verify a specific image maps to the correct platform/version
		g.Expect(images).To(HaveKey("quay.io/hypershift/control-plane-operator-aws:4.17.9"))
		g.Expect(images["quay.io/hypershift/control-plane-operator-aws:4.17.9"]).To(ContainElement("aws/4.17.9"))
	})

	t.Run("When using production overrides, it should return non-empty deduplicated images", func(t *testing.T) {
		g := NewWithT(t)
		images := allOverrideImages(overrides)
		g.Expect(images).ToNot(BeEmpty())

		// Production overrides have many versions pointing to the same image digest,
		// so the number of unique images should be much less than total entries
		totalRefs := 0
		for _, refs := range images {
			totalRefs += len(refs)
		}
		g.Expect(len(images)).To(BeNumerically("<", totalRefs),
			"Expected deduplication: unique images (%d) should be less than total references (%d)",
			len(images), totalRefs)
	})

	t.Run("When using empty overrides, it should return empty map", func(t *testing.T) {
		g := NewWithT(t)
		images := allOverrideImages(&CPOOverrides{})
		g.Expect(images).To(BeEmpty())
	})
}
