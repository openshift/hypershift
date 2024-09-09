package releaseinfo

import (
	"context"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	imagev1 "github.com/openshift/api/image/v1"
)

func TestProviderWithOpenShiftImageRegistryOverridesDecorator_Lookup(t *testing.T) {
	g := NewWithT(t)

	// Create mock resources.
	mirroredReleaseImage := "mirrored-release-image"
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
			canonicalReleaseImage: []string{mirroredReleaseImage},
		},
		lock: sync.Mutex{},
	}

	// Call the Lookup method and validate GetMirroredReleaseImage.
	_, err := provider.Lookup(context.Background(), canonicalReleaseImage, []byte("test-pull-secret"))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(provider.GetMirroredReleaseImage()).To(Equal(mirroredReleaseImage))
}
