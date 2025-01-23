package util

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/opencontainers/go-digest"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
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
	}
}
