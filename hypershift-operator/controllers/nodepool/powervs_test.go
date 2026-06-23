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

	t.Run("When a named stream is specified, it should resolve image from OSStreams", func(t *testing.T) {
		g := NewWithT(t)
		releaseImage := &releaseinfo.ReleaseImage{
			OSStreams: map[string]*stream.Stream{
				"rhel-9": {
					Architectures: map[string]stream.Arch{
						"ppc64le": {
							Images: stream.Images{
								PowerVS: &stream.ReplicatedObject{
									Regions: map[string]stream.SingleObject{
										"us-south": {
											Release: "9.6.20260101.0",
											Object:  "rhcos-96-20260101-0-powervs-ppc64le.ova.gz",
											Bucket:  "rhcos-powervs-images-us-south",
											Url:     "https://rhcos-powervs-images-us-south.s3.us-south.cloud-object-storage.appdomain.cloud/rhcos-96-20260101-0-powervs-ppc64le.ova.gz",
										},
									},
								},
							},
						},
					},
				},
			},
		}
		regionData, cosRegion, err := getPowerVSImage("us-south", releaseImage, "rhel-9")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cosRegion).To(Equal("us-south"))
		g.Expect(regionData).ToNot(BeNil())
		g.Expect(regionData.Object).To(Equal("rhcos-96-20260101-0-powervs-ppc64le.ova.gz"))
	})
}
