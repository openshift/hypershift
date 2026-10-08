package releaseinfo

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestStaticProviderDecoratorLookup(t *testing.T) {
	t.Parallel()

	t.Run("When static images are configured, it should return isolated snapshots without mutating the delegate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		const payloadImage = "payload.example.com/cluster-ingress-operator:latest"
		releaseImage := &ReleaseImage{
			ImageStream: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{
					Tags: []imageapi.TagReference{
						{
							Name:        "cluster-ingress-operator",
							From:        &corev1.ObjectReference{Name: payloadImage},
							Annotations: map[string]string{"source": "payload"},
						},
					},
				},
			},
			canonicalComponentImages: map[string]string{
				"cluster-ingress-operator": payloadImage,
			},
			overriddenComponentImages: sets.New("preexisting-component"),
		}
		provider := &StaticProviderDecorator{
			Delegate: &fakeProvider{
				lookupFn: func(context.Context, string, []byte) (*ReleaseImage, error) {
					return releaseImage, nil
				},
			},
			ComponentImages: map[string]string{
				"cluster-ingress-operator": "override.example.com/cluster-ingress-operator:latest",
			},
		}

		first, err := provider.Lookup(t.Context(), "release.example.com/ocp-release:latest", nil)
		g.Expect(err).ToNot(HaveOccurred())
		second, err := provider.Lookup(t.Context(), "release.example.com/ocp-release:latest", nil)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(first).ToNot(BeIdenticalTo(releaseImage))
		g.Expect(second).ToNot(BeIdenticalTo(first))
		g.Expect(first.ComponentImages()["cluster-ingress-operator"]).To(Equal("override.example.com/cluster-ingress-operator:latest"))
		g.Expect(first.ComponentImageOverridden("cluster-ingress-operator")).To(BeTrue())
		g.Expect(first.ComponentImageOverridden("kube-apiserver")).To(BeFalse())
		g.Expect(first.Spec.Tags).To(HaveLen(2))
		g.Expect(second.Spec.Tags).To(HaveLen(2), "static tags must not accumulate across lookups")

		g.Expect(releaseImage.Spec.Tags).To(HaveLen(1))
		g.Expect(releaseImage.Spec.Tags[0].From.Name).To(Equal(payloadImage))
		g.Expect(releaseImage.overriddenComponentImages).To(Equal(sets.New("preexisting-component")))
		g.Expect(releaseImage.canonicalComponentImages).To(Equal(map[string]string{"cluster-ingress-operator": payloadImage}))

		first.Spec.Tags[0].From.Name = "mutated.example.com/image:latest"
		first.Spec.Tags[0].Annotations["source"] = "mutated"
		first.overriddenComponentImages.Insert("first-only")
		first.canonicalComponentImages["first-only"] = "mutated.example.com/image:latest"

		g.Expect(second.Spec.Tags[0].From.Name).To(Equal(payloadImage))
		g.Expect(second.Spec.Tags[0].Annotations).To(Equal(map[string]string{"source": "payload"}))
		g.Expect(second.overriddenComponentImages.Has("first-only")).To(BeFalse())
		g.Expect(second.canonicalComponentImages).ToNot(HaveKey("first-only"))
		g.Expect(releaseImage.Spec.Tags[0].Annotations).To(Equal(map[string]string{"source": "payload"}))
	})

	t.Run("When no static images are configured, it should preserve nil private metadata", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := &ReleaseImage{ImageStream: &imageapi.ImageStream{}}
		provider := &StaticProviderDecorator{
			Delegate: &fakeProvider{
				lookupFn: func(context.Context, string, []byte) (*ReleaseImage, error) {
					return releaseImage, nil
				},
			},
		}

		result, err := provider.Lookup(t.Context(), "release.example.com/ocp-release:latest", nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).ToNot(BeIdenticalTo(releaseImage))
		g.Expect(result.canonicalComponentImages).To(BeNil())
		g.Expect(result.overriddenComponentImages).To(BeNil())
		g.Expect(result.CanonicalComponentImages()).To(BeEmpty())
	})
}
