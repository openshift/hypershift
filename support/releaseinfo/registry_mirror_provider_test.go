package releaseinfo

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/coreos/stream-metadata-go/stream"
)

func TestRegistryMirrorProviderDecoratorLookup(t *testing.T) {
	releaseImageWithTags := &ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: "hypershift",
						From: &corev1.ObjectReference{
							Name: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:def456",
						},
					},
				},
			},
		},
		StreamMetadata: &stream.Stream{},
		overriddenComponentImages: sets.New(
			"hypershift",
		),
	}

	releaseImageNoTags := &ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			Spec: imageapi.ImageStreamSpec{},
		},
		StreamMetadata: &stream.Stream{},
	}

	tests := []struct {
		name              string
		image             string
		registryOverrides map[string]string
		delegateImage     *ReleaseImage
		delegateErr       error
		wantDelegateImage string
		wantTagName       string
		wantImageOverride bool
		wantErr           bool
		wantErrContains   string
	}{
		{
			name:  "When registry overrides match the release image, it should pass the overridden image to the delegate",
			image: "quay.io/openshift-release-dev/ocp-release-nightly@sha256:abc123",
			registryOverrides: map[string]string{
				"quay.io/openshift-release-dev": "myregistry.example.com/openshift-release-dev",
			},
			delegateImage:     releaseImageWithTags,
			wantDelegateImage: "myregistry.example.com/openshift-release-dev/ocp-release-nightly@sha256:abc123",
			wantTagName:       "myregistry.example.com/openshift-release-dev/ocp-v4.0-art-dev@sha256:def456",
			wantImageOverride: true,
		},
		{
			name:  "When no registry override matches the image, it should pass the original image to the delegate",
			image: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
			registryOverrides: map[string]string{
				"registry.example.com/no-match": "mirror.example.com/no-match",
			},
			delegateImage:     releaseImageWithTags,
			wantDelegateImage: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
			wantImageOverride: true,
		},
		{
			name:              "When registry overrides map is empty, it should pass the original image to the delegate",
			image:             "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
			registryOverrides: map[string]string{},
			delegateImage:     releaseImageNoTags,
			wantDelegateImage: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
		},
		{
			name:  "When the delegate returns an error, it should propagate the error",
			image: "quay.io/org/repo@sha256:abc",
			registryOverrides: map[string]string{
				"quay.io": "mirror.example.com",
			},
			delegateErr:     fmt.Errorf("connection refused"),
			wantErr:         true,
			wantErrContains: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var lookedUpImage string
			provider := &RegistryMirrorProviderDecorator{
				Delegate: &fakeProvider{
					lookupFn: func(_ context.Context, image string, _ []byte) (*ReleaseImage, error) {
						lookedUpImage = image
						if tt.delegateErr != nil {
							return nil, tt.delegateErr
						}
						return tt.delegateImage, nil
					},
				},
				RegistryOverrides: tt.registryOverrides,
				lock:              sync.Mutex{},
			}

			result, err := provider.Lookup(t.Context(), tt.image, []byte(`{"auths":{}}`))

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.wantErrContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.wantErrContains))
				}
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(lookedUpImage).To(Equal(tt.wantDelegateImage))

			if tt.wantTagName != "" {
				g.Expect(result.ImageStream.Spec.Tags[0].From.Name).To(Equal(tt.wantTagName))
			}
			g.Expect(result.ComponentImageOverridden("hypershift")).To(Equal(tt.wantImageOverride))
		})
	}

	t.Run("When composed decorators look up concurrently, it should return isolated snapshots without mutating the delegate", func(t *testing.T) {
		const (
			lookupCount  = 64
			staticImages = 256
		)

		g := NewWithT(t)
		delegateImage := &ReleaseImage{
			ImageStream: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{
					Tags: []imageapi.TagReference{
						{
							Name:        "payload-component",
							From:        &corev1.ObjectReference{Name: "quay.io/example/payload-component:latest"},
							Annotations: map[string]string{"source": "payload"},
						},
					},
				},
			},
		}
		componentImages := make(map[string]string, staticImages)
		for i := 0; i < staticImages; i++ {
			component := fmt.Sprintf("static-component-%d", i)
			componentImages[component] = fmt.Sprintf("quay.io/example/%s:latest", component)
		}
		staticProvider := &StaticProviderDecorator{
			Delegate: &fakeProvider{
				lookupFn: func(context.Context, string, []byte) (*ReleaseImage, error) {
					return delegateImage, nil
				},
			},
			ComponentImages: componentImages,
		}
		providers := []*RegistryMirrorProviderDecorator{
			{
				Delegate:          staticProvider,
				RegistryOverrides: map[string]string{"quay.io": "mirror-a.example.com"},
			},
			{
				Delegate:          staticProvider,
				RegistryOverrides: map[string]string{"quay.io": "mirror-b.example.com"},
			},
		}

		start := make(chan struct{})
		results := make(chan *ReleaseImage, lookupCount)
		errs := make(chan error, lookupCount)
		var wg sync.WaitGroup
		for i := 0; i < lookupCount; i++ {
			wg.Add(1)
			go func(provider *RegistryMirrorProviderDecorator) {
				defer wg.Done()
				<-start
				result, err := provider.Lookup(t.Context(), "quay.io/example/release:latest", nil)
				if err != nil {
					errs <- err
					return
				}
				results <- result
			}(providers[i%len(providers)])
		}
		close(start)
		wg.Wait()
		close(results)
		close(errs)

		g.Expect(errs).To(BeEmpty())
		g.Expect(results).To(HaveLen(lookupCount))
		g.Expect(delegateImage.Spec.Tags).To(HaveLen(1))
		g.Expect(delegateImage.Spec.Tags[0].From.Name).To(Equal("quay.io/example/payload-component:latest"))
		g.Expect(delegateImage.Spec.Tags[0].Annotations).To(Equal(map[string]string{"source": "payload"}))
		g.Expect(delegateImage.canonicalComponentImages).To(BeNil())
		g.Expect(delegateImage.overriddenComponentImages).To(BeNil())

		var snapshots []*ReleaseImage
		for result := range results {
			g.Expect(result.Spec.Tags).To(HaveLen(staticImages + 1))
			g.Expect(result.overriddenComponentImages).To(HaveLen(staticImages))
			snapshots = append(snapshots, result)
		}
		first, second := snapshots[0], snapshots[1]
		first.Spec.Tags[0].Annotations["source"] = "mutated"
		first.overriddenComponentImages.Insert("first-only")
		first.canonicalComponentImages["first-only"] = "mutated.example.com/image:latest"

		g.Expect(second.Spec.Tags[0].Annotations).To(Equal(map[string]string{"source": "payload"}))
		g.Expect(second.overriddenComponentImages.Has("first-only")).To(BeFalse())
		g.Expect(second.canonicalComponentImages).ToNot(HaveKey("first-only"))
	})
}

// fakeProvider is a test helper that delegates Lookup to a caller-supplied function.
type fakeProvider struct {
	lookupFn func(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error)
}

func (f *fakeProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	return f.lookupFn(ctx, image, pullSecret)
}
