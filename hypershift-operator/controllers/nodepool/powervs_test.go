package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/coreos/stream-metadata-go/stream"
)

func TestGetPowerVSImage(t *testing.T) {
	testCases := []struct {
		name            string
		region          string
		streamName      string
		releaseImage    *releaseinfo.ReleaseImage
		expectedError   string
		expectedRelease string
		expectedRegion  string
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
		{
			name:       "When named stream is used with multi-stream ReleaseImage it should resolve from the named stream",
			region:     "us-south",
			streamName: "rhel-9",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"ppc64le": {
							Images: stream.Images{
								PowerVS: &stream.ReplicatedObject{
									Regions: map[string]stream.SingleObject{
										"us-south": {
											Release: "default-4.18.0-ppc64le",
											Object:  "default-object",
											Bucket:  "default-bucket",
										},
									},
								},
							},
						},
					},
				},
				OSStreams: map[string]*stream.Stream{
					"rhel-9": {
						Architectures: map[string]stream.Arch{
							"ppc64le": {
								Images: stream.Images{
									PowerVS: &stream.ReplicatedObject{
										Regions: map[string]stream.SingleObject{
											"us-south": {
												Release: "rhel9-4.18.0-ppc64le",
												Object:  "rhel9-object",
												Bucket:  "rhel9-bucket",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRelease: "rhel9-4-18-0-ppc64le",
			expectedRegion:  "us-south",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			img, cosRegion, err := getPowerVSImage(tc.region, tc.releaseImage, tc.streamName)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(img).ToNot(BeNil())
			g.Expect(img.Release).To(Equal(tc.expectedRelease))
			g.Expect(cosRegion).To(Equal(tc.expectedRegion))
		})
	}
}
