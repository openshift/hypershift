package releaseinfo

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/coreos/stream-metadata-go/stream"
	"github.com/docker/distribution"
)

func TestProviderWithOpenShiftImageRegistryOverridesDecorator_Lookup(t *testing.T) {
	g := NewWithT(t)

	// Create mock resources.
	mirroredReleaseImage := "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64"
	canonicalReleaseImage := "canonical-release-image"
	releaseImage := &ReleaseImage{
		ImageStream:    &imagev1.ImageStream{},
		StreamMetadata: &stream.Stream{},
	}

	// Create registry providers delegating to a cached provider so we can mock the cache content for the mirroredReleaseImage.
	delegate := &RegistryMirrorProviderDecorator{
		Delegate: &CachedProvider{
			Inner: &RegistryClientProvider{},
			Cache: map[string]*ReleaseImage{
				mirroredReleaseImage: releaseImage,
			},
		},
		RegistryOverrides: map[string]string{},
	}
	provider := &ProviderWithOpenShiftImageRegistryOverridesDecorator{
		Delegate: delegate,
		OpenShiftImageRegistryOverrides: map[string][]string{
			canonicalReleaseImage: {mirroredReleaseImage},
		},
		// Mock repoSetupFn to avoid real network calls for mirror verification.
		repoSetupFn: func(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error) {
			ref, err := reference.Parse(imageRef)
			if err != nil {
				return nil, nil, err
			}
			return nil, &ref, nil
		},
		lock: sync.Mutex{},
	}

	pullSecret := []byte(`{"auths":{}}`)
	// Call the Lookup method and validate GetMirroredReleaseImage.
	_, err := provider.Lookup(t.Context(), canonicalReleaseImage, pullSecret)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(provider.GetMirroredReleaseImage()).To(Equal(mirroredReleaseImage))
}

func TestProviderWithOpenShiftImageRegistryOverridesDecorator_LookupWithNilRepoSetupFn(t *testing.T) {
	g := NewWithT(t)

	directImage := "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64"
	releaseImage := &ReleaseImage{
		ImageStream:    &imagev1.ImageStream{},
		StreamMetadata: &stream.Stream{},
	}

	delegate := &RegistryMirrorProviderDecorator{
		Delegate: &CachedProvider{
			Inner: &RegistryClientProvider{},
			Cache: map[string]*ReleaseImage{
				directImage: releaseImage,
			},
		},
		RegistryOverrides: map[string]string{},
	}

	// When repoSetupFn is nil it should default to registryclient.GetRepoSetup.
	// Use an image that does not match any override so the default repoSetupFn
	// is assigned but never called, avoiding real network calls.
	provider := &ProviderWithOpenShiftImageRegistryOverridesDecorator{
		Delegate: delegate,
		OpenShiftImageRegistryOverrides: map[string][]string{
			"no-match-source": {"no-match-mirror"},
		},
		// repoSetupFn intentionally nil to exercise the default fallback.
		lock: sync.Mutex{},
	}

	pullSecret := []byte(`{"auths":{}}`)
	result, err := provider.Lookup(t.Context(), directImage, pullSecret)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(releaseImage))
	g.Expect(provider.GetMirroredReleaseImage()).To(BeEmpty())
}

func TestProviderWithOpenShiftImageRegistryOverridesDecorator_GetOpenShiftImageRegistryOverrides(t *testing.T) {
	t.Run("When the returned snapshot is modified, it should preserve the provider state", func(t *testing.T) {
		g := NewWithT(t)
		provider := &ProviderWithOpenShiftImageRegistryOverridesDecorator{}
		input := map[string][]string{
			"quay.io": {"mirror-a.example.com", "mirror-b.example.com"},
		}
		provider.SetOpenShiftImageRegistryOverrides(input)

		input["quay.io"][0] = "mutated-input.example.com"
		input["new-source.example.com"] = []string{"new-mirror.example.com"}

		overrides := provider.GetOpenShiftImageRegistryOverrides()
		overrides["quay.io"][0] = "mutated.example.com"
		delete(overrides, "quay.io")

		g.Expect(provider.GetOpenShiftImageRegistryOverrides()).To(Equal(map[string][]string{
			"quay.io": {"mirror-a.example.com", "mirror-b.example.com"},
		}))
	})
}

func TestProviderWithOpenShiftImageRegistryOverridesDecorator_ConcurrentOverrides(t *testing.T) {
	t.Run("When mirror overrides are refreshed during lookup, it should use synchronized snapshots", func(t *testing.T) {
		const waitTimeout = time.Second
		canonicalReleaseImage := "canonical-release-image"
		oldMirrorImage := "mirror-a.example.com/release:latest"
		newMirrorImage := "mirror-b.example.com/release:latest"
		releaseImage := &ReleaseImage{
			ImageStream:    &imagev1.ImageStream{},
			StreamMetadata: &stream.Stream{},
		}
		requestedImages := make([]string, 0, 2)
		lookupStarted := make(chan struct{}, 1)
		releaseLookup := make(chan struct{})
		lookupDone := make(chan struct{})
		lookupErr := make(chan error, 1)
		releaseLookupClosed := false
		defer func() {
			if !releaseLookupClosed {
				close(releaseLookup)
			}
		}()

		provider := &ProviderWithOpenShiftImageRegistryOverridesDecorator{
			Delegate: &RegistryMirrorProviderDecorator{
				Delegate: &fakeProvider{
					lookupFn: func(_ context.Context, image string, _ []byte) (*ReleaseImage, error) {
						requestedImages = append(requestedImages, image)
						if image == oldMirrorImage {
							lookupStarted <- struct{}{}
							<-releaseLookup
						}
						return releaseImage, nil
					},
				},
				RegistryOverrides: map[string]string{},
			},
			repoSetupFn: func(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error) {
				ref, err := reference.Parse(imageRef)
				if err != nil {
					return nil, nil, err
				}
				return nil, &ref, nil
			},
		}
		provider.SetOpenShiftImageRegistryOverrides(map[string][]string{
			canonicalReleaseImage: {oldMirrorImage},
		})

		go func() {
			defer close(lookupDone)
			_, err := provider.Lookup(t.Context(), canonicalReleaseImage, []byte(`{"auths":{}}`))
			lookupErr <- err
		}()

		select {
		case <-lookupStarted:
		case <-time.After(waitTimeout):
			t.Fatal("timed out waiting for the delegate lookup to block")
		}

		setDone := make(chan struct{})
		go func() {
			provider.SetOpenShiftImageRegistryOverrides(map[string][]string{
				canonicalReleaseImage: {newMirrorImage},
			})
			close(setDone)
		}()

		select {
		case <-setDone:
		case <-time.After(waitTimeout):
			t.Fatal("override publication waited for the blocked delegate lookup")
		}

		close(releaseLookup)
		releaseLookupClosed = true
		select {
		case <-lookupDone:
		case <-time.After(waitTimeout):
			t.Fatal("timed out waiting for the first lookup to finish")
		}

		g := NewWithT(t)
		g.Expect(<-lookupErr).ToNot(HaveOccurred())
		g.Expect(requestedImages).To(Equal([]string{oldMirrorImage}))

		_, err := provider.Lookup(t.Context(), canonicalReleaseImage, []byte(`{"auths":{}}`))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(requestedImages).To(Equal([]string{oldMirrorImage, newMirrorImage}))
		g.Expect(provider.GetMirroredReleaseImage()).To(Equal(newMirrorImage))
	})
}
