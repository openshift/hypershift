package releaseinfo

import (
	"context"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/docker/distribution"
)

func TestProviderWithOpenShiftImageRegistryOverridesDecorator_Lookup(t *testing.T) {
	g := NewWithT(t)

	// Create mock resources.
	mirroredReleaseImage := "quay.io/openshift-release-dev/ocp-release:4.16.13-x86_64"
	canonicalReleaseImage := "canonical-release-image"
	releaseImage := &ReleaseImage{
		ImageStream:    &imagev1.ImageStream{},
		StreamMetadata: &CoreOSStreamMetadata{},
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
		StreamMetadata: &CoreOSStreamMetadata{},
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
