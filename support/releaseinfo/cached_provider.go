package releaseinfo

import (
	"context"
	"sync"
	"time"
)

var _ Provider = (*CachedProvider)(nil)

// CachedProvider maintains a simple cache of release image info and only queries
// the embedded provider when there is no cache hit.
type CachedProvider struct {
	Cache map[string]*ReleaseImage
	Inner Provider
	mu    sync.Mutex

	once sync.Once
}

func (p *CachedProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (releaseImage *ReleaseImage, err error) {
	// Purge the cache every once in a while as a simple leak mitigation
	p.once.Do(func() {
		go func() {
			t := time.NewTicker(30 * time.Minute)
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					p.mu.Lock()
					p.Cache = map[string]*ReleaseImage{}
					p.mu.Unlock()
				}
			}
		}()
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.Cache[image]; ok {
		return entry, nil
	}
	entry, err := p.Inner.Lookup(ctx, image, pullSecret)
	if err != nil {
		return nil, err
	}
	p.Cache[image] = entry
	return entry, nil
}
