package util

import (
	"context"
	"fmt"
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
		name             string
		imageRef         string
		pullSecret       []byte
		expectedErr      bool
		expectedCacheHit bool
	}{
		{
			name:             "if failed to parse image reference",
			imageRef:         "invalid-image-ref",
			pullSecret:       pullSecret,
			expectedErr:      true,
			expectedCacheHit: false,
		},
		{
			name:             "Pull x86 manifest",
			imageRef:         "quay.io/openshift-release-dev/ocp-release:4.16.12-x86_64",
			pullSecret:       pullSecret,
			expectedErr:      false,
			expectedCacheHit: true,
		},
		{
			name:             "Pull x86 manifest from cache",
			imageRef:         "quay.io/openshift-release-dev/ocp-release:4.16.12-x86_64",
			pullSecret:       pullSecret,
			expectedErr:      false,
			expectedCacheHit: true,
		},
		{
			name:             "Pull Multiarch manifest",
			imageRef:         "quay.io/openshift-release-dev/ocp-release:4.16.12-multi",
			pullSecret:       pullSecret,
			expectedErr:      false,
			expectedCacheHit: true,
		},
		{
			name:             "Pull Multiarch manifest with Shah",
			imageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:be8bcea2ab176321a4e1e54caab4709f9024bc437e52ca5bc088e729367cd0cf",
			pullSecret:       pullSecret,
			expectedErr:      false,
			expectedCacheHit: true,
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

			parsedImageRef, err := reference.Parse(tc.imageRef)
			g.Expect(err).NotTo(HaveOccurred())
			_, exists := manifestsCache.Get(parsedImageRef.String())
			g.Expect(exists).To(Equal(tc.expectedCacheHit))
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
			name:      "if no overrides are provided",
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
				Registry:  "myregistry1.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
		},
		{
			name:      "if registry override partial coincidence is found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "mce-image",
				Namespace: "mce",
				Tag:       "latest",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "mce-image",
				Namespace: "mce",
				Tag:       "latest",
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
				ID:        "sha256:ba93b7791accfb38e76634edbc815d596ebf39c3d4683a001f8286b3e122ae69",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "virthost.ostest.test.metalkube.org:5000",
				Name:      "local-release-image",
				Namespace: "localimages",
				ID:        "sha256:ba93b7791accfb38e76634edbc815d596ebf39c3d4683a001f8286b3e122ae69",
			},
		},
		{
			name:      "if registry override partial coincidence is found, and using ID",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "mce-image",
				Namespace: "mce",
				ID:        "sha256:ba93b7791accfb38e76634edbc815d596ebf39c3d4683a001f8286b3e122ae69",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "mce-image",
				Namespace: "mce",
				ID:        "sha256:ba93b7791accfb38e76634edbc815d596ebf39c3d4683a001f8286b3e122ae69",
			},
		},
		{
			name:      "if only the root registry is provided",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				ID:        "sha256:f225d0f0fd7d4509ed00e82f11c871731ee04aecff7d924f820ac6dba7c7b346",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "virthost.ostest.test.metalkube.org:5000",
				Name:      "release",
				Namespace: "ocp",
				ID:        "sha256:f225d0f0fd7d4509ed00e82f11c871731ee04aecff7d924f820ac6dba7c7b346",
			},
		},
		{
			name:      "if only the root registry is provided and multiple mirrors are provided",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build03.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				ID:        "sha256:f225d0f0fd7d4509ed00e82f11c871731ee04aecff7d924f820ac6dba7c7b346",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry1.io",
				Name:      "release",
				Namespace: "ocp",
				ID:        "sha256:f225d0f0fd7d4509ed00e82f11c871731ee04aecff7d924f820ac6dba7c7b346",
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			g := NewGomegaWithT(t)
			imgRef := seekOverride(ctx, tc.overrides, tc.imageRef)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef), fmt.Sprintf("Expected image reference to be equal to: %v, \nbut got: %v", tc.expectedImgRef, imgRef))
		})
	}
}

func fakeOverrides() map[string][]string {
	return map[string][]string{
		"quay.io/openshift-release-dev/ocp-release": {
			"myregistry1.io/openshift-release-dev/ocp-release",
			"myregistry2.io/openshift-release-dev/ocp-release",
		},
		"quay.io/mce": {
			"myregistry.io/mce",
		},
		"registry.build01.ci.openshift.org/ci-op-p2mqdwjp/release": {
			"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image",
		},
		"registry.ci.openshift.org/ocp/4.18-2025-01-04-031500": {
			"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image",
		},
		"registry.build02.ci.openshift.org": {
			"virthost.ostest.test.metalkube.org:5000",
		},
		"registry.build03.ci.openshift.org": {
			"myregistry1.io",
			"myregistry2.io",
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
				Name: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
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
