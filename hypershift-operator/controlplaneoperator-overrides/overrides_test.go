package controlplaneoperatoroverrides

import (
	_ "embed"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

func TestCPOImage(t *testing.T) {
	overrides = fakeOverrides()
	initOverridesByVersion()
	tests := []struct {
		version  string
		expected string
	}{
		{
			version:  "4.17.8",
			expected: "quay.io/hypershift/control-plane-operator:4.17.8",
		},
		{
			version:  "4.15.9",
			expected: "quay.io/hypershift/control-plane-operator:4.15.9",
		},
		{
			version:  "4.13.9",
			expected: "",
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i+1), func(t *testing.T) {
			g := NewWithT(t)
			actual := CPOImage(test.version)
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestLatestOverrideTestReleases(t *testing.T) {
	overrides = fakeOverrides()
	initOverridesByVersion()
	g := NewWithT(t)
	resultLatest, resultPrevious := LatestOverrideTestReleases()
	g.Expect(resultLatest).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"))
	g.Expect(resultPrevious).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"))
}

func TestLoadOverrides(t *testing.T) {
	g := NewWithT(t)
	o, err := loadOverrides(overridesSampleYAML)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(o).ToNot(BeNil())
	g.Expect(o.Overrides).To(HaveLen(2))
	g.Expect(o.Testing.Previous).To(Equal("quay.io/openshift-release-dev/ocp-release:4.16.16-x86_64"))
	g.Expect(o.Testing.Latest).To(Equal("quay.io/openshift-release-dev/ocp-release:4.16.17-x86_64"))
	g.Expect(o.Overrides[1].Version).To(Equal("4.16.18"))
	g.Expect(o.Overrides[1].CPOImage).To(Equal("quay.io/hypershift/hypershift-cpo:patch"))
	g.Expect(o.Overrides[0].Version).To(Equal("4.16.17"))
	g.Expect(o.Overrides[0].CPOImage).To(Equal("quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:1a50894aafa6b750bf890ef147a20699ff5b807e586d15506426a8a615580797"))
}

func fakeOverrides() *CPOOverrides {
	result := &CPOOverrides{
		Testing: CPOOverrideTestReleases{
			Latest:   fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"),
			Previous: fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"),
		},
	}
	for _, minor := range []int{15, 16, 17} {
		for patch := range 10 {
			result.Overrides = append(result.Overrides, CPOOverride{
				Version:  fmt.Sprintf("4.%d.%d", minor, patch),
				CPOImage: fmt.Sprintf("quay.io/hypershift/control-plane-operator:4.%d.%d", minor, patch),
			})
		}
	}
	return result
}

//go:embed assets/overrides_sample.yaml
var overridesSampleYAML []byte
