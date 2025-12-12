package util

import (
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
)

func TestGetRegistryOverrides(t *testing.T) {
	ctx := t.Context()
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		source         string
		mirror         string
		expectedImgRef *reference.DockerImageReference
		expectAnErr    bool
		overrideFound  bool
	}{
		{
			name: "if failed to parse source image",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source:         "",
			mirror:         "",
			expectedImgRef: nil,
			expectAnErr:    true,
			overrideFound:  false,
		},
		{
			name: "if registry override coincidence not found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			mirror: "myregistry.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr:   false,
			overrideFound: false,
		},
		{
			name: "if registry override coincidence is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			mirror: "myregistry.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr:   false,
			overrideFound: true,
		},
		{
			name: "if registry override partial coincidence is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				Tag:       "multiarch",
			},
			source: "quay.io/mce",
			mirror: "quay.io/openshifttest",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				Tag:       "multiarch",
			},
			expectAnErr:   false,
			overrideFound: true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := GetRegistryOverrides(ctx, tc.ref, tc.source, tc.mirror)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
		})
	}
}

func TestSeekOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		overrides      map[string][]string
		imageRef       reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
	}{
		{
			name:      "if no overrides are provided, and multi mirrors",
			overrides: map[string][]string{},
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
		},
		{
			name:      "if registry override exact coincidence is found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				Tag:       "4.15.0-rc.0-multi",
			},
		},
		{
			name:      "if registry override partial coincidence is found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				Tag:       "multiarch",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				Tag:       "multiarch",
			},
		},
		{
			name:      "if registry override coincidence is not found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "testimage",
				Namespace: "test-namespace",
				Tag:       "latest",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "testimage",
				Namespace: "test-namespace",
				Tag:       "latest",
			},
		},
		{
			name:      "if failed to find registry override",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "cnv-image",
				Namespace: "cnv",
				Tag:       "latest",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "cnv-image",
				Namespace: "cnv",
				Tag:       "latest",
			},
		},
		{
			name:      "if registry override exact coincidence is found, and using ID",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build01.ci.openshift.org",
				Name:      "release",
				Namespace: "ci-op-p2mqdwjp",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
		},
		{
			//busybox@sha256:c5439d7db88ab5423999530349d327b04279ad3161d7596d2126dfb5b02bfd1f
			name:      "if registry override partial coincidence is found, and using ID",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				ID:        "sha256:c5439d7db88ab5423999530349d327b04279ad3161d7596d2126dfb5b02bfd1f",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				ID:        "sha256:c5439d7db88ab5423999530349d327b04279ad3161d7596d2126dfb5b02bfd1f",
			},
		},
		{
			name:      "if only the root registry is provided",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
		},
		{
			name:      "if only the root registry is provided and multiple mirrors are provided",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build03.ci.openshift.org",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			g := NewGomegaWithT(t)
			pullSecret, err := os.ReadFile("../../hack/dev/fakePullSecret.json")
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			imgRef := SeekOverride(ctx, tc.overrides, tc.imageRef, pullSecret)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef), fmt.Sprintf("Expected image reference to be equal to: %v, \nbut got: %v", tc.expectedImgRef, imgRef))
		})
	}
}

func fakeOverrides() map[string][]string {
	return map[string][]string{
		"quay.io/openshift-release-dev/ocp-release": {
			"myregistry1.io/openshift-release-dev/ocp-release",
			"quay.io/openshifttest/ocp-release",
		},
		"quay.io/mce": {
			"quay.io/openshifttest",
		},
		"registry.build01.ci.openshift.org/ci-op-p2mqdwjp/release": {
			"quay.io/openshifttest/ocp-release",
		},
		"registry.ci.openshift.org/ocp/4.18-2025-01-04-031500": {
			"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image",
		},
		"registry.build02.ci.openshift.org": {
			"quay.io",
		},
		"registry.build03.ci.openshift.org": {
			"myregistry1.io",
			"myregistry2.io",
			"quay.io",
		},
		"quay.io/prometheus": {
			"brew.registry.redhat.io/prometheus",
		},
	}
}

func TestTryOnlyNamespaceOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if namespace override is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry: "quay.io",
				Name:     "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
				Name:     "openshift-release-dev",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if namespace override is not found - namespace not empty",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Namespace: "test",
				Name:      "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if namespace override is not found - name mismatch",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "different-namespace",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryOnlyNamespaceOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestTryExactCoincidenceOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if exact coincidence override is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry:  "myregistry.io",
				Namespace: "openshift-release-dev",
				Name:      "ocp-release",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if exact coincidence override is not found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "different-name",
				Namespace: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryExactCoincidenceOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			if tc.overrideFound {
				g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			} else {
				g.Expect(imgRef).To(BeNil())
			}
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestTryOnlyRootRegistryOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if root registry override is found",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Name: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "virthost.ostest.test.metalkube.org:5000",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if root registry override is not found - namespace not empty",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Namespace: "test",
				Name:      "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if root registry override is not found - registry not empty",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry: "test",
				Name:     "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if root registry override is not found - name mismatch",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "different-registry",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryOnlyRootRegistryOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			if tc.overrideFound {
				g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			} else {
				g.Expect(imgRef).To(BeNil())
			}
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}
