package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
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
			mirror: "my_registry/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr: false,
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
			mirror: "my_registry/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "",
				Name:      "openshift-release-dev/ocp-release",
				Namespace: "my_registry",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr: false,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, err := GetRegistryOverrides(ctx, tc.ref, tc.source, tc.mirror)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}
