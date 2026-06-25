package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/coreos/stream-metadata-go/stream"
)

func TestGetPowerVSImage(t *testing.T) {
	testCases := []struct {
		name          string
		region        string
		releaseImage  *releaseinfo.ReleaseImage
		expectedError string
	}{
		{
			name:   "When PowerVS images is nil, it should return error",
			region: "us-south",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"ppc64le": {
							Images: stream.Images{},
						},
					},
				},
			},
			expectedError: "release image metadata has no PowerVS images",
		},
		{
			name:   "When architecture is not found, it should return error",
			region: "us-south",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{},
				},
			},
			expectedError: "couldn't find OS metadata for architecture",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			_, _, err := getPowerVSImage(tc.region, tc.releaseImage, "")
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tc.expectedError))
		})
	}
}
