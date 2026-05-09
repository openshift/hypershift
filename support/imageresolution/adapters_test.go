package imageresolution

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	hyperutil "github.com/openshift/hypershift/support/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestProviderSet_Config_RegistryOverrides(t *testing.T) {
	t.Run("When registry overrides are configured, it should return them", func(t *testing.T) {
		g := NewWithT(t)

		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{"quay.io": "mirror.io"},
		}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		ps := &ProviderSet{resolver: resolver}

		g.Expect(ps.Config().RegistryOverrides).To(Equal(map[string]string{"quay.io": "mirror.io"}))
	})
}

func TestProviderSet_Config_ImageRegistryMirrors(t *testing.T) {
	t.Run("When image registry mirrors are configured, it should return them", func(t *testing.T) {
		g := NewWithT(t)

		cfg := ResolverConfig{
			ImageRegistryMirrors: map[string][]string{
				"quay.io/openshift": {"mirror.io/openshift"},
			},
		}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		ps := &ProviderSet{resolver: resolver}

		g.Expect(ps.Config().ImageRegistryMirrors).To(Equal(map[string][]string{
			"quay.io/openshift": {"mirror.io/openshift"},
		}))
	})
}

func TestProviderSet_ImageMetadataProvider(t *testing.T) {
	t.Run("When ImageMetadata is called, it should delegate to the metadataProvider", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeMetadataFetcher{
			configs: map[string]*dockerv1client.DockerImageConfig{
				"quay.io/img:latest": {Comment: "test-image"},
			},
		}
		metaProvider := newMetadataProvider(resolver, fetcher, newMetadataCache(0))
		ps := &ProviderSet{metadata: metaProvider, resolver: resolver}

		adapter := ps.ImageMetadataProvider()
		config, err := adapter.ImageMetadata(context.Background(), "quay.io/img:latest", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(config.Comment).To(Equal("test-image"))
	})
}

func TestProviderSet_ImageMetadataProvider_InternalAdapter(t *testing.T) {
	t.Run("When ImageMetadataProvider is called without test override, it should return the internal adapter", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			ForDataPlane().
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		provider := ps.ImageMetadataProvider()
		g.Expect(provider).ToNot(BeNil())

		// The returned provider should be the imageMetadataProviderAdapter, not a test override.
		_, isAdapter := provider.(*imageMetadataProviderAdapter)
		g.Expect(isAdapter).To(BeTrue())
	})

	t.Run("When GetManifest is called on the adapter, it should delegate to the raw provider without panicking", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			ForDataPlane().
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		provider := ps.ImageMetadataProvider()
		g.Expect(provider).ToNot(BeNil())

		// The raw delegate has no real registry connection, so this will return an error,
		// but the point is it executes without panic, proving the delegation works.
		manifest, err := provider.GetManifest(t.Context(), "quay.io/openshift/test:latest", []byte("{}"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(manifest).To(BeNil())
	})

	t.Run("When GetDigest is called on the adapter, it should delegate to the raw provider without panicking", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			ForDataPlane().
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		provider := ps.ImageMetadataProvider()
		g.Expect(provider).ToNot(BeNil())

		digest, ref, err := provider.GetDigest(t.Context(), "quay.io/openshift/test:latest", []byte("{}"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(digest).To(BeEmpty())
		g.Expect(ref).To(BeNil())
	})

	t.Run("When GetMetadata is called on the adapter, it should delegate to the raw provider without panicking", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			ForDataPlane().
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		provider := ps.ImageMetadataProvider()
		g.Expect(provider).ToNot(BeNil())

		config, descriptors, blobStore, err := provider.GetMetadata(t.Context(), "quay.io/openshift/test:latest", []byte("{}"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(config).To(BeNil())
		g.Expect(descriptors).To(BeNil())
		g.Expect(blobStore).To(BeNil())
	})

	t.Run("When GetOverride is called on the adapter, it should delegate to the raw provider without panicking", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			ForDataPlane().
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		provider := ps.ImageMetadataProvider()
		g.Expect(provider).ToNot(BeNil())

		// GetOverride parses the image ref and returns the override; with no mirrors configured
		// it returns the original ref without error.
		ref, err := provider.GetOverride(t.Context(), "quay.io/openshift/test:latest", []byte("{}"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ref).ToNot(BeNil())
		g.Expect(ref.String()).To(ContainSubstring("quay.io"))
	})
}

func TestProviderSet_WithImageMetadataProvider(t *testing.T) {
	t.Run("When WithImageMetadataProvider is used, it should return the injected provider", func(t *testing.T) {
		g := NewWithT(t)

		fakeProvider := &fakeImageMetadataProvider{
			config: &dockerv1client.DockerImageConfig{Comment: "injected-provider"},
		}

		ps, err := NewProviderSet().
			ForDataPlane().
			WithImageMetadataProvider(fakeProvider).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		provider := ps.ImageMetadataProvider()
		g.Expect(provider).ToNot(BeNil())
		g.Expect(provider).To(BeIdenticalTo(fakeProvider))

		config, err := provider.ImageMetadata(t.Context(), "any-image:latest", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(config.Comment).To(Equal("injected-provider"))
	})
}

// fakeImageMetadataProvider is a minimal fake implementing hyperutil.ImageMetadataProvider
// for tests that only need to verify injection through WithImageMetadataProvider.
type fakeImageMetadataProvider struct {
	hyperutil.ImageMetadataProvider
	config *dockerv1client.DockerImageConfig
}

func (f *fakeImageMetadataProvider) ImageMetadata(_ context.Context, _ string, _ []byte) (*dockerv1client.DockerImageConfig, error) {
	return f.config, nil
}

func TestProviderSet_Reconcile(t *testing.T) {
	t.Run("When mirror refresh returns new mirrors, it should update the config", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			WithRegistryOverrides(map[string]string{"quay.io": "mirror.io"}).
			WithMirrorRefresh(func(_ context.Context, _ crclient.Client) (map[string][]string, error) {
				return map[string][]string{
					"quay.io/openshift": {"new-mirror.io/openshift"},
				}, nil
			}).
			WithReleaseFetcher(&fakeReleaseFetcher{releases: map[string]*ReleaseImage{}}).
			WithMetadataFetcher(&fakeMetadataFetcher{configs: map[string]*dockerv1client.DockerImageConfig{}}).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		err = ps.Reconcile(context.Background(), nil)
		g.Expect(err).ToNot(HaveOccurred())

		cfg := ps.Config()
		g.Expect(cfg.ImageRegistryMirrors).To(Equal(map[string][]string{
			"quay.io/openshift": {"new-mirror.io/openshift"},
		}))
		g.Expect(cfg.RegistryOverrides).To(Equal(map[string]string{"quay.io": "mirror.io"}))
	})

	t.Run("When no mirror refresh is configured, it should be a no-op", func(t *testing.T) {
		g := NewWithT(t)

		ps, err := NewProviderSet().
			ForDataPlane().
			WithReleaseFetcher(&fakeReleaseFetcher{releases: map[string]*ReleaseImage{}}).
			WithMetadataFetcher(&fakeMetadataFetcher{configs: map[string]*dockerv1client.DockerImageConfig{}}).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		err = ps.Reconcile(context.Background(), nil)
		g.Expect(err).ToNot(HaveOccurred())
	})
}
