package imageresolution

import (
	"context"
	"fmt"

	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
)

type imageMetadataFetcher interface {
	fetchConfig(ctx context.Context, ref string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)
}

type metadataProvider struct {
	resolver *imageResolver
	fetcher  imageMetadataFetcher
	cache    *metadataCache
}

func newMetadataProvider(resolver *imageResolver, fetcher imageMetadataFetcher, cache *metadataCache) *metadataProvider {
	return &metadataProvider{
		resolver: resolver,
		fetcher:  fetcher,
		cache:    cache,
	}
}

func (p *metadataProvider) ImageMetadata(
	ctx context.Context, ref string, pullSecret []byte,
) (*dockerv1client.DockerImageConfig, error) {
	resolved, err := p.resolver.resolveForDirectFetch(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolving image reference: %w", err)
	}

	if cached, ok := p.cache.get(resolved); ok {
		return cached.(*dockerv1client.DockerImageConfig), nil
	}

	config, err := p.fetcher.fetchConfig(ctx, resolved, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("fetching image config from %s: %w", resolved, err)
	}

	p.cache.put(resolved, config)
	return config, nil
}
