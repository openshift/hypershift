package imageresolution

import (
	"context"
	"fmt"
	"maps"
	"sync"
)

type releaseFetcher interface {
	fetch(ctx context.Context, pullSpec string, pullSecret []byte) (*ReleaseImage, error)
}

type releaseInfoProvider struct {
	resolver       *imageResolver
	fetcher        releaseFetcher
	imageOverrides map[string]string
	cache          *releaseCache

	mirroredMu           sync.RWMutex
	mirroredReleaseImage string
}

func newReleaseInfoProvider(
	resolver *imageResolver,
	fetcher releaseFetcher,
	imageOverrides map[string]string,
	cache *releaseCache,
) *releaseInfoProvider {
	return &releaseInfoProvider{
		resolver:       resolver,
		fetcher:        fetcher,
		imageOverrides: imageOverrides,
		cache:          cache,
	}
}

func (p *releaseInfoProvider) Lookup(
	ctx context.Context, pullSpec string, pullSecret []byte,
) (*ReleaseImage, error) {
	if cached := p.cache.get(pullSpec); cached != nil {
		return cloneReleaseImage(cached), nil
	}

	resolved, err := p.resolver.resolveForDirectFetch(ctx, pullSpec)
	if err != nil {
		return nil, fmt.Errorf("resolving release image: %w", err)
	}

	p.mirroredMu.Lock()
	if resolved != pullSpec {
		p.mirroredReleaseImage = resolved
	} else {
		p.mirroredReleaseImage = ""
	}
	p.mirroredMu.Unlock()

	release, err := p.fetcher.fetch(ctx, resolved, pullSecret)
	if err != nil {
		p.mirroredMu.Lock()
		p.mirroredReleaseImage = ""
		p.mirroredMu.Unlock()
		return nil, fmt.Errorf("fetching release from %s: %w", resolved, err)
	}

	release = cloneReleaseImage(release)

	for name, img := range release.ComponentImages {
		resolved, err := p.resolver.resolveForPodSpec(ctx, img)
		if err != nil {
			return nil, fmt.Errorf("resolving component image %s: %w", name, err)
		}
		release.ComponentImages[name] = resolved
	}

	maps.Copy(release.ComponentImages, p.imageOverrides)

	p.cache.put(pullSpec, cloneReleaseImage(release))
	return release, nil
}
