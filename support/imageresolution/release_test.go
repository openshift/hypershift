package imageresolution

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

type fakeReleaseFetcher struct {
	releases map[string]*ReleaseImage
}

func (f *fakeReleaseFetcher) fetch(_ context.Context, pullSpec string, _ []byte) (*ReleaseImage, error) {
	r, ok := f.releases[pullSpec]
	if !ok {
		return nil, fmt.Errorf("release not found: %s", pullSpec)
	}
	return r, nil
}

func TestReleaseInfoProvider_Lookup(t *testing.T) {
	ctx := context.Background()
	pullSecret := []byte("secret")

	t.Run("When release pullspec is overridden, it should fetch from resolved location using resolveForDirectFetch", func(t *testing.T) {
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
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"mirror.io/ocp:4.17": {
					ComponentImages: map[string]string{
						"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:a",
					},
				},
			},
		}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))

		release, err := provider.Lookup(ctx, "quay.io/ocp:4.17", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(release.ComponentImages["kube-apiserver"]).To(
			Equal("mirror.io/openshift/kube-apiserver@sha256:a"))
	})

	t.Run("When image overrides are configured, it should apply them as final step", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"quay.io/ocp:4.17": {
					ComponentImages: map[string]string{
						"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:a",
					},
				},
			},
		}
		imageOverrides := map[string]string{
			"kube-apiserver": "custom-registry.io/custom-kas:latest",
		}
		provider := newReleaseInfoProvider(resolver, fetcher, imageOverrides, newReleaseCache(0))

		release, err := provider.Lookup(ctx, "quay.io/ocp:4.17", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(release.ComponentImages["kube-apiserver"]).To(
			Equal("custom-registry.io/custom-kas:latest"))
	})

	t.Run("When release is cached, it should not call fetcher", func(t *testing.T) {
		g := NewWithT(t)

		callCount := 0
		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"quay.io/ocp:4.17": {
					ComponentImages: map[string]string{},
				},
			},
		}
		countingFetcher := &countingReleaseFetcher{
			delegate:  fetcher,
			callCount: &callCount,
		}
		provider := newReleaseInfoProvider(resolver, countingFetcher, nil, newReleaseCache(time.Hour))

		_, err := provider.Lookup(ctx, "quay.io/ocp:4.17", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(Equal(1))

		_, err = provider.Lookup(ctx, "quay.io/ocp:4.17", pullSecret)
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
		fetcher := &fakeReleaseFetcher{releases: map[string]*ReleaseImage{}}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))

		_, err := provider.Lookup(ctx, "nonexistent:latest", pullSecret)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When resolver fails during direct fetch, Lookup should return a resolving error", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			&failingConfigSource{},
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeReleaseFetcher{releases: map[string]*ReleaseImage{}}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))

		_, err := provider.Lookup(t.Context(), "quay.io/ocp:4.17", pullSecret)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(ContainSubstring("resolving release image")))
		g.Expect(err).To(MatchError(ContainSubstring("config source unavailable")))
	})

	t.Run("When fetcher fails after resolving to a mirror, it should clear mirroredReleaseImage", func(t *testing.T) {
		g := NewWithT(t)

		// Set up registry overrides so the resolved URL differs from the original,
		// causing mirroredReleaseImage to be set before the fetch attempt.
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
		// The fetcher has no entry for the mirror URL, so fetch will fail.
		fetcher := &fakeReleaseFetcher{releases: map[string]*ReleaseImage{}}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))

		_, err := provider.Lookup(ctx, "quay.io/ocp:4.17", pullSecret)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("fetching release from mirror.io/ocp:4.17"))

		// The mirrored release image should have been cleared on error.
		provider.mirroredMu.RLock()
		defer provider.mirroredMu.RUnlock()
		g.Expect(provider.mirroredReleaseImage).To(BeEmpty(),
			"mirroredReleaseImage should be cleared when the fetch fails")
	})

	t.Run("When resolver fails during component image resolution, Lookup should return a component image error", func(t *testing.T) {
		g := NewWithT(t)

		// Use a mutableConfigSource that succeeds on the first call (resolveForDirectFetch)
		// but fails on the second call (resolveForPodSpec for component images).
		source := &callCountingConfigSource{
			failAfter: 1,
			config:    ResolverConfig{},
		}
		resolver := newImageResolver(
			source,
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"quay.io/ocp:4.17": {
					ComponentImages: map[string]string{
						"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:a",
					},
				},
			},
		}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))

		_, err := provider.Lookup(t.Context(), "quay.io/ocp:4.17", pullSecret)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(ContainSubstring("resolving component image")))
		g.Expect(err).To(MatchError(ContainSubstring("kube-apiserver")))
		g.Expect(err).To(MatchError(ContainSubstring("config source unavailable")))
	})
}

type countingReleaseFetcher struct {
	delegate  releaseFetcher
	callCount *int
}

func (c *countingReleaseFetcher) fetch(ctx context.Context, pullSpec string, pullSecret []byte) (*ReleaseImage, error) {
	*c.callCount++
	return c.delegate.fetch(ctx, pullSpec, pullSecret)
}

func TestDualProviderSetInvariant(t *testing.T) {
	ctx := context.Background()
	pullSecret := []byte("secret")

	mgmtOverrides := map[string]string{"quay.io": "mgmt-mirror.azurecr.io"}
	mirrors := map[string][]string{
		"quay.io/openshift": {"mirror.shared.io/openshift"},
	}
	sharedChecker := &fakeMirrorChecker{
		available: map[string]bool{"mirror.shared.io": true},
	}

	cpResolver := newImageResolver(
		newStaticConfigSource(ResolverConfig{
			RegistryOverrides:    mgmtOverrides,
			ImageRegistryMirrors: mirrors,
		}),
		sharedChecker,
		newMirrorAvailabilityCache(0),
	)

	// Data-plane resolver has ICSP/IDMS mirrors (for fetching) but no CLI overrides.
	dpResolver := newImageResolver(
		newStaticConfigSource(ResolverConfig{
			ImageRegistryMirrors: mirrors,
		}),
		sharedChecker,
		newMirrorAvailabilityCache(0),
	)

	fetcher := &fakeReleaseFetcher{
		releases: map[string]*ReleaseImage{
			"mgmt-mirror.azurecr.io/openshift/ocp:4.17": {
				ComponentImages: map[string]string{
					"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:a",
				},
			},
			"mirror.shared.io/openshift/ocp:4.17": {
				ComponentImages: map[string]string{
					"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:a",
				},
			},
			"quay.io/openshift/ocp:4.17": {
				ComponentImages: map[string]string{
					"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:a",
				},
			},
		},
	}

	cpProvider := newReleaseInfoProvider(cpResolver, fetcher, nil, newReleaseCache(0))
	dpProvider := newReleaseInfoProvider(dpResolver, fetcher, nil, newReleaseCache(0))

	t.Run("When data-plane provider has no overrides, component images should not contain mgmt-cluster prefixes", func(t *testing.T) {
		g := NewWithT(t)

		release, err := dpProvider.Lookup(ctx, "quay.io/openshift/ocp:4.17", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())

		for name, img := range release.ComponentImages {
			g.Expect(img).ToNot(ContainSubstring("mgmt-mirror.azurecr.io"),
				"data-plane component %s should not contain management-cluster override prefix", name)
		}
	})

	t.Run("When data-plane provider has ICSP/IDMS mirrors, it should fetch the release image via mirror", func(t *testing.T) {
		g := NewWithT(t)

		release, err := dpProvider.Lookup(ctx, "quay.io/openshift/ocp:4.17", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(release).ToNot(BeNil())
		g.Expect(dpProvider.mirroredReleaseImage).To(Equal("mirror.shared.io/openshift/ocp:4.17"),
			"data-plane provider should fetch via ICSP/IDMS mirror")
	})

	t.Run("When CP provider has CLI overrides, component images should contain mgmt-cluster prefixes", func(t *testing.T) {
		g := NewWithT(t)

		release, err := cpProvider.Lookup(ctx, "quay.io/openshift/ocp:4.17", pullSecret)
		g.Expect(err).ToNot(HaveOccurred())

		kas := release.ComponentImages["kube-apiserver"]
		g.Expect(kas).To(ContainSubstring("mgmt-mirror.azurecr.io"),
			"control-plane component should contain management-cluster override prefix")
	})
}
