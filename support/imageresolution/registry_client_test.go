package imageresolution

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConvertReleaseImage(t *testing.T) {
	t.Run("When release image has ImageStream and StreamMetadata, it should preserve both", func(t *testing.T) {
		g := NewWithT(t)

		ri := &releaseinfo.ReleaseImage{
			ImageStream: &imageapi.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name: "4.17.0",
					Annotations: map[string]string{
						"io.openshift.build.versions": "kubernetes=1.30.0",
					},
				},
				Spec: imageapi.ImageStreamSpec{
					Tags: []imageapi.TagReference{
						{
							Name: "kube-apiserver",
							From: &corev1.ObjectReference{Name: "quay.io/openshift/kube-apiserver@sha256:abc"},
						},
					},
				},
			},
			StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
				Stream: "4.17",
				Architectures: map[string]releaseinfo.CoreOSArchitecture{
					"x86_64": {},
				},
			},
		}

		result := convertReleaseImage(ri)

		g.Expect(result.ImageStream).ToNot(BeNil())
		g.Expect(result.ImageStream.Name).To(Equal("4.17.0"))
		g.Expect(result.StreamMetadata).ToNot(BeNil())
		g.Expect(result.StreamMetadata.Stream).To(Equal("4.17"))
	})

	t.Run("When release image has nil ImageStream, it should produce nil ImageStream and empty maps", func(t *testing.T) {
		g := NewWithT(t)

		ri := &releaseinfo.ReleaseImage{
			ImageStream:    nil,
			StreamMetadata: nil,
		}

		result := convertReleaseImage(ri)

		g.Expect(result.ImageStream).To(BeNil())
		g.Expect(result.StreamMetadata).To(BeNil())
		g.Expect(result.ComponentImages).To(BeEmpty())
		g.Expect(result.ComponentVersions).To(BeEmpty())
	})

	t.Run("When ImageStream is deep-copied, modifying the original should not affect the result", func(t *testing.T) {
		g := NewWithT(t)

		ri := &releaseinfo.ReleaseImage{
			ImageStream: &imageapi.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
				Spec: imageapi.ImageStreamSpec{
					Tags: []imageapi.TagReference{
						{
							Name: "etcd",
							From: &corev1.ObjectReference{Name: "quay.io/etcd@sha256:def"},
						},
					},
				},
			},
		}

		result := convertReleaseImage(ri)
		ri.ImageStream.Name = "mutated"

		g.Expect(result.ImageStream.Name).To(Equal("4.17.0"))
	})
}
