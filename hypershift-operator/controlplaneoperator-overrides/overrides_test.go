package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

func TestCPOImage(t *testing.T) {
	tests := []struct {
		platform string
		version  string
		expected string
	}{
		{
			platform: "AWS",
			version:  "4.17.8",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.8",
		},
		{
			platform: "aws",
			version:  "4.17.9",
			expected: "quay.io/hypershift/control-plane-operator-aws:4.17.9",
		},
		{
			platform: "Azure",
			version:  "4.17.9",
			expected: "quay.io/hypershift/control-plane-operator-azure:4.17.9",
		},
		{
			platform: "aws",
			version:  "4.13.9",
			expected: "",
		},
		{
			platform: "foo",
			version:  "4.17.9",
			expected: "",
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i+1), func(t *testing.T) {
			g := NewWithT(t)
			actual := getCPOImage(test.platform, test.version, getOverridesByPlatformAndVersion(fakeOverrides()))
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
	g := NewWithT(t)
	o, err := loadOverrides(overridesYAML)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(o).ToNot(BeNil())
	g.Expect(o.Platforms.AWS.Overrides).ToNot(BeEmpty())
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
	result.Platforms.Azure.Overrides = generateOverrides("azure")
	return result
}

//go:embed assets/overrides_sample.yaml
var overridesSampleYAML []byte
