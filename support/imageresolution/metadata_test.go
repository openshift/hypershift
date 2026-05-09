package imageresolution

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
)

type fakeMetadataFetcher struct {
	configs map[string]*dockerv1client.DockerImageConfig
}

func (f *fakeMetadataFetcher) fetchConfig(_ context.Context, ref string, _ []byte) (*dockerv1client.DockerImageConfig, error) {
	c, ok := f.configs[ref]
	if !ok {
		return nil, fmt.Errorf("config not found: %s", ref)
	}
	return c, nil
}

func TestMetadataProvider_ImageMetadata(t *testing.T) {
	ctx := context.Background()
	pullSecret := []byte("secret")

	t.Run("When ref is overridden, it should fetch from resolved location via resolveForDirectFetch", func(t *testing.T) {
		g := NewWithT(t)

		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{
				"quay.io": "mirror.io",
			},
		}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		expectedConfig := &dockerv1client.DockerImageConfig{}
		fetcher := &fakeMetadataFetcher{
			configs: map[string]*dockerv1client.DockerImageConfig{
				"mirror.io/foo@sha256:abc": expectedConfig,
			},
		}
		provider := newMetadataProvider(resolver, fetcher, newMetadataCache(0))

		config, err := provider.ImageMetadata(ctx, "quay.io/foo@sha256:abc", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(config).To(Equal(expectedConfig))
	})

	t.Run("When metadata is cached, it should not call fetcher", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		callCount := 0
		expectedConfig := &dockerv1client.DockerImageConfig{}
		fetcher := &countingMetadataFetcher{
			delegate: &fakeMetadataFetcher{
				configs: map[string]*dockerv1client.DockerImageConfig{
					"quay.io/foo@sha256:abc": expectedConfig,
				},
			},
			callCount: &callCount,
		}
		provider := newMetadataProvider(resolver, fetcher, newMetadataCache(time.Hour))

		_, err := provider.ImageMetadata(ctx, "quay.io/foo@sha256:abc", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(Equal(1))

		_, err = provider.ImageMetadata(ctx, "quay.io/foo@sha256:abc", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(Equal(1))
	})

	t.Run("When fetcher fails, it should return error", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeMetadataFetcher{configs: map[string]*dockerv1client.DockerImageConfig{}}
		provider := newMetadataProvider(resolver, fetcher, newMetadataCache(0))

		_, err := provider.ImageMetadata(ctx, "nonexistent", pullSecret)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When resolver fails during direct fetch, ImageMetadata should return a resolving error", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			&failingConfigSource{},
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeMetadataFetcher{configs: map[string]*dockerv1client.DockerImageConfig{}}
		provider := newMetadataProvider(resolver, fetcher, newMetadataCache(0))

		_, err := provider.ImageMetadata(t.Context(), "quay.io/foo@sha256:abc", pullSecret)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(ContainSubstring("resolving image reference")))
		g.Expect(err).To(MatchError(ContainSubstring("config source unavailable")))
	})
}

type countingMetadataFetcher struct {
	delegate  imageMetadataFetcher
	callCount *int
}

func (c *countingMetadataFetcher) fetchConfig(ctx context.Context, ref string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	*c.callCount++
	return c.delegate.fetchConfig(ctx, ref, pullSecret)
}
