package ignitionserver

import (
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/gomega"
)

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
			name: "repository match not found",
			overrides: map[string][]string{
				"quay.io/openshift-release-dev/ocp-release": {
					"myregistry1.io/openshift-release-dev/ocp-release",
				},
			},
			image:       "quay.io/test-namespace/testimage:latest",
			expectedImg: "quay.io/test-namespace/testimage:latest",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			g := NewGomegaWithT(t)
			pullSecret, err := os.ReadFile("../../../../../hack/dev/fakePullSecret.json")
			if err != nil {
				t.Fatalf("failed to read pull secret file: %v", err)
			}
			img, _ := lookupMappedImage(ctx, tc.overrides, tc.image, pullSecret)
			g.Expect(img).To(Equal(tc.expectedImg), fmt.Sprintf("Expected image reference to be equal to: %s, \nbut got: %s", tc.expectedImg, img))
		})
	}
}
