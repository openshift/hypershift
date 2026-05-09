package imageresolution

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultReleaseCacheTTL  = 30 * time.Minute
	defaultMirrorCacheTTL   = 15 * time.Second
	defaultMetadataCacheTTL = 12 * time.Hour
)

// MirrorRefreshFunc fetches current image registry mirrors from the cluster.
type MirrorRefreshFunc func(ctx context.Context, client crclient.Client) (map[string][]string, error)

// ProviderSet bundles release, metadata, and image resolution providers built from a single configuration.
type ProviderSet struct {
	release              *releaseInfoProvider
	metadata             *metadataProvider
	resolver             *imageResolver
	rawMetadata          *hyperutil.RegistryClientImageMetadataProvider
	rawMetadataMu        sync.RWMutex
	configSource         *mutableConfigSource
	mirrorRefresh        MirrorRefreshFunc
	testMetadataProvider hyperutil.ImageMetadataProvider
}

// Config returns a snapshot of the current resolver configuration.
func (ps *ProviderSet) Config() ResolverConfig {
	if ps.resolver == nil {
		return ResolverConfig{}
	}
	cfg, _ := ps.resolver.configSource.current(context.Background())
	return cfg
}

// Lookup resolves a release image, applying registry overrides and mirrors.
func (ps *ProviderSet) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	ri, err := ps.release.Lookup(ctx, image, pullSecret)
	if err != nil {
		return nil, err
	}

	// Apply resolved component images back to ImageStream tags so that
	// releaseinfo.ReleaseImage.ComponentImages() returns overridden refs.
	if ri.ImageStream != nil {
		for i, tag := range ri.ImageStream.Spec.Tags {
			if resolved, ok := ri.ComponentImages[tag.Name]; ok {
				ri.ImageStream.Spec.Tags[i].From.Name = resolved
			}
		}
	}

	return &releaseinfo.ReleaseImage{
		ImageStream:    ri.ImageStream,
		StreamMetadata: ri.StreamMetadata,
	}, nil
}

// GetMirroredReleaseImage returns the mirrored release image from the last Lookup, or empty if no mirror was used.
func (ps *ProviderSet) GetMirroredReleaseImage() string {
	ps.release.mirroredMu.RLock()
	defer ps.release.mirroredMu.RUnlock()
	return ps.release.mirroredReleaseImage
}

// Reconcile refreshes image registry mirrors from the cluster when a MirrorRefreshFunc was provided.
func (ps *ProviderSet) Reconcile(ctx context.Context, client crclient.Client) error {
	if ps.mirrorRefresh == nil {
		return nil
	}
	mirrors, err := ps.mirrorRefresh(ctx, client)
	if err != nil {
		return fmt.Errorf("refreshing image registry mirrors: %w", err)
	}
	ps.configSource.updateMirrors(mirrors)
	ps.rawMetadataMu.Lock()
	if ps.rawMetadata != nil {
		ps.rawMetadata.OpenShiftImageRegistryOverrides = mirrors
	}
	ps.rawMetadataMu.Unlock()
	return nil
}

// ProviderSetBuilder configures and constructs a ProviderSet using a builder pattern.
type ProviderSetBuilder struct {
	registryOverrides map[string]string
	mirrors           map[string][]string
	imageOverrides    map[string]string
	forDataPlane      bool
	hasStaticMirrors  bool
	mirrorRefresh     MirrorRefreshFunc

	releaseFetcher       releaseFetcher
	metadataFetcher      imageMetadataFetcher
	mirrorChecker        mirrorAvailabilityChecker
	testMetadataProvider hyperutil.ImageMetadataProvider
}

// NewProviderSet returns a new builder for constructing a ProviderSet.
func NewProviderSet() *ProviderSetBuilder {
	return &ProviderSetBuilder{}
}

// WithRegistryOverrides sets CLI registry overrides (source→destination) applied to all image references.
func (b *ProviderSetBuilder) WithRegistryOverrides(overrides map[string]string) *ProviderSetBuilder {
	b.registryOverrides = cloneStringMap(overrides)
	return b
}

// WithImageRegistryMirrors sets static image registry mirrors (source→mirror list) for direct-fetch resolution.
func (b *ProviderSetBuilder) WithImageRegistryMirrors(mirrors map[string][]string) *ProviderSetBuilder {
	b.mirrors = cloneStringSliceMap(mirrors)
	b.hasStaticMirrors = len(mirrors) > 0
	return b
}

// WithMirrorRefresh sets a function that dynamically refreshes mirrors from the cluster during Reconcile.
func (b *ProviderSetBuilder) WithMirrorRefresh(fn MirrorRefreshFunc) *ProviderSetBuilder {
	b.mirrorRefresh = fn
	return b
}

// WithImageOverrides sets per-component image overrides that take precedence over release payload images.
func (b *ProviderSetBuilder) WithImageOverrides(overrides map[string]string) *ProviderSetBuilder {
	b.imageOverrides = cloneStringMap(overrides)
	return b
}

// ForDataPlane marks this builder as a data-plane provider, disabling CLI registry overrides,
// dynamic mirror refresh, and per-component image overrides. Static ICSP/IDMS mirrors are still
// allowed because the CPO needs them to fetch the release image from the management cluster network.
func (b *ProviderSetBuilder) ForDataPlane() *ProviderSetBuilder {
	b.forDataPlane = true
	return b
}

// WithReleaseProvider injects a releaseinfo.Provider as the release fetcher,
// wrapping it into the internal releaseFetcher interface. Primarily for testing.
func (b *ProviderSetBuilder) WithReleaseProvider(p releaseinfo.Provider) *ProviderSetBuilder {
	b.releaseFetcher = &providerReleaseFetcher{provider: p}
	return b
}

// WithReleaseFetcher overrides the default registry-based release fetcher, primarily for testing.
func (b *ProviderSetBuilder) WithReleaseFetcher(f releaseFetcher) *ProviderSetBuilder {
	b.releaseFetcher = f
	return b
}

// WithMetadataFetcher overrides the default registry-based metadata fetcher, primarily for testing.
func (b *ProviderSetBuilder) WithMetadataFetcher(f imageMetadataFetcher) *ProviderSetBuilder {
	b.metadataFetcher = f
	return b
}

// WithImageMetadataProvider injects a complete ImageMetadataProvider, primarily for testing.
// When set, ImageMetadataProvider() returns this directly instead of the internal adapter.
func (b *ProviderSetBuilder) WithImageMetadataProvider(p hyperutil.ImageMetadataProvider) *ProviderSetBuilder {
	b.testMetadataProvider = p
	return b
}

// WithMirrorChecker overrides the default HTTP mirror availability checker, primarily for testing.
func (b *ProviderSetBuilder) WithMirrorChecker(c mirrorAvailabilityChecker) *ProviderSetBuilder {
	b.mirrorChecker = c
	return b
}

// Build validates the builder configuration and constructs the ProviderSet.
func (b *ProviderSetBuilder) Build() (*ProviderSet, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}

	source := newMutableConfigSource(ResolverConfig{
		RegistryOverrides:    b.registryOverrides,
		ImageRegistryMirrors: b.mirrors,
	})

	checker := b.mirrorChecker
	if checker == nil {
		if b.hasStaticMirrors || b.mirrorRefresh != nil {
			checker = newHTTPMirrorChecker()
		} else {
			checker = &noopMirrorChecker{}
		}
	}

	resolver := newImageResolver(
		source,
		checker,
		newMirrorAvailabilityCache(defaultMirrorCacheTTL),
	)

	rf := b.releaseFetcher
	if rf == nil {
		rf = newRegistryReleaseFetcher()
	}

	mf := b.metadataFetcher
	if mf == nil {
		mf = newRegistryMetadataFetcher()
	}

	releaseProvider := newReleaseInfoProvider(
		resolver,
		rf,
		b.imageOverrides,
		newReleaseCache(defaultReleaseCacheTTL),
	)

	metaProvider := newMetadataProvider(
		resolver,
		mf,
		newMetadataCache(defaultMetadataCacheTTL),
	)

	rawMeta := &hyperutil.RegistryClientImageMetadataProvider{
		OpenShiftImageRegistryOverrides: b.mirrors,
	}

	return &ProviderSet{
		release:              releaseProvider,
		metadata:             metaProvider,
		resolver:             resolver,
		rawMetadata:          rawMeta,
		configSource:         source,
		mirrorRefresh:        b.mirrorRefresh,
		testMetadataProvider: b.testMetadataProvider,
	}, nil
}

func (b *ProviderSetBuilder) validate() error {
	if b.forDataPlane {
		if len(b.registryOverrides) > 0 {
			return fmt.Errorf("ForDataPlane() is incompatible with WithRegistryOverrides()")
		}
		if b.mirrorRefresh != nil {
			return fmt.Errorf("ForDataPlane() is incompatible with WithMirrorRefresh()")
		}
		if len(b.imageOverrides) > 0 {
			return fmt.Errorf("ForDataPlane() is incompatible with WithImageOverrides()")
		}
	}

	return nil
}

type noopMirrorChecker struct{}

func (n *noopMirrorChecker) isAvailable(_ context.Context, _ string) bool {
	return false
}

// providerReleaseFetcher adapts a releaseinfo.Provider to the internal releaseFetcher interface.
type providerReleaseFetcher struct {
	provider releaseinfo.Provider
}

func (f *providerReleaseFetcher) fetch(ctx context.Context, pullSpec string, pullSecret []byte) (*ReleaseImage, error) {
	ri, err := f.provider.Lookup(ctx, pullSpec, pullSecret)
	if err != nil {
		return nil, err
	}
	return convertReleaseImage(ri), nil
}
