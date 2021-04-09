package releaseinfo

import (
	"context"
	"sync"
)

var _ Provider = (*CachedProvider)(nil)

// CachedProvider maintains a simple cache of release image info and only queries
// the embedded provider when there is no cache hit.
type CachedProvider struct {
	Cache map[string]*ReleaseImage
	Inner Provider
	mu    sync.Mutex
}

func (p *CachedProvider) Lookup(ctx context.Context, image string) (releaseImage *ReleaseImage, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.Cache[image]; ok {
		return entry, nil
	}
	entry, err := p.Inner.Lookup(ctx, image)
	if err != nil {
		return nil, err
	}
	p.Cache[image] = entry
	return entry, nil
}
