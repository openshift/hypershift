package util

import (
	"context"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	"github.com/opencontainers/go-digest"
)

func TestGetRegistryOverrides(t *testing.T) {
	ctx := context.TODO()
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

func TestGetManifest(t *testing.T) {
	ctx := context.TODO()
	pullSecret := []byte("{}")

	testsCases := []struct {
		name           string
		imageRef       string
		pullSecret     []byte
		expectedErr    bool
		validateCache  bool
		expectedDigest digest.Digest
	}{
		{
			name:        "if failed to parse image reference",
			imageRef:    "invalid-image-ref",
			pullSecret:  pullSecret,
			expectedErr: true,
		},
		{
			name:        "Pull x86 manifest",
			imageRef:    "quay.io/openshift-release-dev/ocp-release:4.16.12-x86_64",
			pullSecret:  pullSecret,
			expectedErr: false,
		},
		{
			name:           "Pull x86 manifest from cache",
			imageRef:       "quay.io/openshift-release-dev/ocp-release:4.16.12-x86_64",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:2a50e5d5267916078145731db740bbc85ee764e1a194715fd986ab5bf9a3414e",
		},
		{
			name:           "Pull Multiarch manifest",
			imageRef:       "quay.io/openshift-release-dev/ocp-release:4.16.12-multi",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:727276732f03d8d5a2374efa3d01fb0ed9f65b32488b862e9a9d2ff4cde89ff6",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			provider := &RegistryClientImageMetadataProvider{
				OpenShiftImageRegistryOverrides: map[string][]string{},
			}

			manifest, err := provider.GetManifest(ctx, tc.imageRef, tc.pullSecret)
			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(manifest).NotTo(BeNil())
			}

			if tc.validateCache {
				_, exists := manifestsCache.Get(tc.expectedDigest)
				g.Expect(exists).To(BeTrue())
			}
		})
	}
}

func TestGetDigest(t *testing.T) {
	ctx := context.TODO()
	pullSecret := []byte("{}")

	testsCases := []struct {
		name           string
		imageRef       string
		pullSecret     []byte
		expectedErr    bool
		validateCache  bool
		expectedDigest digest.Digest
	}{
		{
			name:        "if failed to parse image reference",
			imageRef:    "::invalid-image-ref",
			pullSecret:  pullSecret,
			expectedErr: true,
		},
		{
			name:           "Multiaarch image digest",
			imageRef:       "quay.io/openshift-release-dev/ocp-release:4.16.12-multi",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:727276732f03d8d5a2374efa3d01fb0ed9f65b32488b862e9a9d2ff4cde89ff6",
		},
		{
			name:           "amd64 Image digest is found in cache",
			imageRef:       "quay.io/openshift-release-dev/ocp-release:4.16.12-x86_64",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:2a50e5d5267916078145731db740bbc85ee764e1a194715fd986ab5bf9a3414e",
		},
		{
			name:           "amd64 Image digest, recover from cache",
			imageRef:       "quay.io/openshift-release-dev/ocp-release:4.16.12-x86_64",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:2a50e5d5267916078145731db740bbc85ee764e1a194715fd986ab5bf9a3414e",
		},
		{
			name:           "Image with digest and not tag",
			imageRef:       "quay.io/openshift-release-dev/ocp-release@sha256:e96047c50caf0aaffeaf7ed0fe50bd3f574ad347cd0f588a56b876f79cc29d3e",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:e96047c50caf0aaffeaf7ed0fe50bd3f574ad347cd0f588a56b876f79cc29d3e",
		},
		{
			name:           "Image not present in overriden registry, falling back to original imageRef",
			imageRef:       "quay.io/prometheus/busybox:latest",
			pullSecret:     pullSecret,
			expectedErr:    false,
			validateCache:  true,
			expectedDigest: "sha256:dfa54ef35e438b9e71ac5549159074576b6382f95ce1a434088e05fd6b730bc4",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			provider := &RegistryClientImageMetadataProvider{
				OpenShiftImageRegistryOverrides: map[string][]string{},
			}

			digest, ref, err := provider.GetDigest(ctx, tc.imageRef, tc.pullSecret)
			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(digest).To(Equal(tc.expectedDigest))
				g.Expect(ref).NotTo(BeNil())
			}

			if tc.validateCache {
				_, exists := digestCache.Get(tc.imageRef)
				g.Expect(exists).To(BeTrue())
			}
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
			ctx := context.TODO()
			g := NewGomegaWithT(t)
			pullSecret, err := os.ReadFile("../../hack/dev/fakePullSecret.json")
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			imgRef := seekOverride(ctx, tc.overrides, tc.imageRef, pullSecret)
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
