package util

import (
	"context"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/docker/distribution"
)

// mockGetMetadataFn is a mock function for testing that simulates image availability
// It returns success for the first mirror and failure for others to test fallback behavior
func mockGetMetadataFn(ctx context.Context, imageRef string, pullSecret []byte) ([]distribution.Descriptor, distribution.BlobStore, error) {
	// Simulate that the first mirror is unavailable and the second is available
	// This allows us to test the fallback logic without reaching out to actual registries
	if imageRef == "myregistry1.io/openshift-release-dev/ocp-release@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0" {
		return nil, nil, fmt.Errorf("image not available: %s", imageRef)
	}
	if imageRef == "quay.io/openshifttest/ocp-release@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0" {
		return []distribution.Descriptor{}, nil, nil
	}
	if imageRef == "quay.io/openshifttest/ocp-release-2@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0" {
		return []distribution.Descriptor{}, nil, nil
	}
	if imageRef == "myregistry1.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi" {
		return nil, nil, fmt.Errorf("image not available: %s", imageRef)
	}
	if imageRef == "quay.io/openshifttest/ocp-release:4.15.0-rc.0-multi" {
		return []distribution.Descriptor{}, nil, nil
	}
	// Return error for other images to simulate unavailability
	return nil, nil, fmt.Errorf("image not available: %s", imageRef)
}

func TestLookupMappedImage(t *testing.T) {
	testsCases := []struct {
		name        string
		overrides   map[string][]string
		image       string
		expectedImg string
	}{
		{
			name:        "no overrides provided",
			overrides:   map[string][]string{},
			image:       "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImg: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
		},
		{
			name: "exact repository match found, and multiple mirrors",
			overrides: map[string][]string{
				"quay.io/openshift-release-dev/ocp-release": {
					"myregistry1.io/openshift-release-dev/ocp-release",
					"quay.io/openshifttest/ocp-release",
				},
			},
			image:       "quay.io/openshift-release-dev/ocp-release@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			expectedImg: "quay.io/openshifttest/ocp-release@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
		},
		{
			name: "exact repository match found, and multiple mirrors, tag",
			overrides: map[string][]string{
				"quay.io/openshift-release-dev/ocp-release": {
					"myregistry1.io/openshift-release-dev/ocp-release",
					"quay.io/openshifttest/ocp-release",
				},
			},
			image:       "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImg: "quay.io/openshifttest/ocp-release:4.15.0-rc.0-multi",
		},
		{
			name: "repository match not found",
			overrides: map[string][]string{
				"quay.io/openshift-release-dev/ocp-release": {
					"myregistry1.io/openshift-release-dev/ocp-release",
				},
			},
			image:       "quay.io/test-namespace/testimage:latest",
			expectedImg: "quay.io/test-namespace/testimage:latest",
		},
		{
			name: "multiple mirrors available, return first available one",
			overrides: map[string][]string{
				"quay.io/openshift-release-dev/ocp-release": {
					"myregistry1.io/openshift-release-dev/ocp-release",
					"quay.io/openshifttest/ocp-release-2",
					"quay.io/openshifttest/ocp-release",
				},
			},
			image:       "quay.io/openshift-release-dev/ocp-release@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			expectedImg: "quay.io/openshifttest/ocp-release-2@sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			g := NewGomegaWithT(t)
			pullSecret, err := os.ReadFile("../../hack/dev/fakePullSecret.json")
			if err != nil {
				t.Fatalf("failed to read pull secret file: %v", err)
			}
			img, _ := LookupMappedImage(ctx, tc.overrides, tc.image, pullSecret, mockGetMetadataFn)
			g.Expect(img).To(Equal(tc.expectedImg), fmt.Sprintf("Expected image reference to be equal to: %s, \nbut got: %s", tc.expectedImg, img))
		})
	}
}
