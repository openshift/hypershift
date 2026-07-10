package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetRHELStreamForBootImage(t *testing.T) {
	testCases := []struct {
		name           string
		nodePool       *hyperv1.NodePool
		releaseImage   *releaseinfo.ReleaseImage
		expectedStream string
		expectErr      bool
	}{
		{
			name: "When spec.osImageStream.Name is rhel-10 and version is 5.x, it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When spec.osImageStream.Name is rhel-9 and version is 4.x, it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When spec.osImageStream.Name is rhel-9 and version is 5.x, it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When spec.osImageStream.Name is rhel-10 and version is 4.x, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectErr: true,
		},
		{
			name: "When spec.osImageStream.Name is invalid, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-8"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectErr: true,
		},
		{
			name: "When spec.osImageStream.Name is empty and version is 4.x, it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When spec.osImageStream.Name is empty and version is 5.x, it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When spec.osImageStream.Name is empty and version is 6.x, it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "6.1.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When spec.osImageStream.Name is empty and version is unparsable, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "not-a-version"}},
			},
			expectErr: true,
		},
		{
			name: "When spec.osImageStream.Name is set and version is unparsable, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "not-a-version"}},
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			stream, err := getRHELStreamForBootImage(tc.nodePool, tc.releaseImage)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(stream).To(Equal(tc.expectedStream))
		})
	}
}

func TestValidateOSImageStream(t *testing.T) {
	testCases := []struct {
		name         string
		nodePool     *hyperv1.NodePool
		releaseImage *releaseinfo.ReleaseImage
		expectErr    bool
	}{
		{
			name: "When osImageStream.Name is empty, it should succeed",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
		},
		{
			name: "When osImageStream.Name is rhel-9 and version is 4.x, it should succeed",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
		},
		{
			name: "When osImageStream.Name is rhel-10 and version is 5.x, it should succeed",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
		},
		{
			name: "When osImageStream.Name is rhel-10 and version is 4.x, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectErr: true,
		},
		{
			name: "When osImageStream.Name is invalid, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-8"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			err := validateOSImageStream(tc.nodePool, tc.releaseImage)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
