package imageresolution

import (
	"maps"
	"sync"
	"time"

	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"
)

func cloneReleaseImage(in *ReleaseImage) *ReleaseImage {
	if in == nil {
		return nil
	}
	out := &ReleaseImage{
		ComponentImages:   make(map[string]string, len(in.ComponentImages)),
		ComponentVersions: make(map[string]string, len(in.ComponentVersions)),
		StreamMetadata:    in.StreamMetadata,
	}
	maps.Copy(out.ComponentImages, in.ComponentImages)
	maps.Copy(out.ComponentVersions, in.ComponentVersions)
	if in.ImageStream != nil {
		out.ImageStream = in.ImageStream.DeepCopy()
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// ReleaseImage holds the parsed contents of a release image payload.
// This is the internal type used within the imageresolution package; ProviderSet.Lookup
// converts it to the external releaseinfo.ReleaseImage before returning to callers.
type ReleaseImage struct {
	ComponentImages   map[string]string
	ComponentVersions map[string]string
	ImageStream       *imageapi.ImageStream
	StreamMetadata    *releaseinfo.CoreOSStreamMetadata
}

type releaseCacheEntry struct {
	release   *ReleaseImage
	timestamp time.Time
}

type releaseCache struct {
	mu      sync.RWMutex
	entries map[string]*releaseCacheEntry
	ttl     time.Duration
}

func newReleaseCache(ttl time.Duration) *releaseCache {
	return &releaseCache{
		entries: make(map[string]*releaseCacheEntry),
		ttl:     ttl,
	}
}

func (c *releaseCache) get(key string) *ReleaseImage {
	c.mu.RLock()
	entry, ok := c.entries[key]
	expired := ok && time.Since(entry.timestamp) > c.ttl
	if ok && !expired {
		release := entry.release
		c.mu.RUnlock()
		return release
	}
	c.mu.RUnlock()

	if expired {
		c.mu.Lock()
		if entry, ok := c.entries[key]; ok && time.Since(entry.timestamp) > c.ttl {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *releaseCache) put(key string, release *ReleaseImage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &releaseCacheEntry{
		release:   release,
		timestamp: time.Now(),
	}
}

type mirrorCacheEntry struct {
	available bool
	timestamp time.Time
}

type mirrorAvailabilityCache struct {
	mu      sync.RWMutex
	entries map[string]*mirrorCacheEntry
	ttl     time.Duration
}

func newMirrorAvailabilityCache(ttl time.Duration) *mirrorAvailabilityCache {
	return &mirrorAvailabilityCache{
		entries: make(map[string]*mirrorCacheEntry),
		ttl:     ttl,
	}
}

func (c *mirrorAvailabilityCache) get(mirror string) (available bool, ok bool) {
	c.mu.RLock()
	entry, exists := c.entries[mirror]
	expired := exists && time.Since(entry.timestamp) > c.ttl
	if exists && !expired {
		avail := entry.available
		c.mu.RUnlock()
		return avail, true
	}
	c.mu.RUnlock()

	if expired {
		c.mu.Lock()
		if entry, ok := c.entries[mirror]; ok && time.Since(entry.timestamp) > c.ttl {
			delete(c.entries, mirror)
		}
		c.mu.Unlock()
	}
	return false, false
}

func (c *mirrorAvailabilityCache) set(mirror string, available bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[mirror] = &mirrorCacheEntry{
		available: available,
		timestamp: time.Now(),
	}
}

type metadataCacheEntry struct {
	value     any
	timestamp time.Time
}

type metadataCache struct {
	mu      sync.RWMutex
	entries map[string]*metadataCacheEntry
	ttl     time.Duration
}

func newMetadataCache(ttl time.Duration) *metadataCache {
	return &metadataCache{
		entries: make(map[string]*metadataCacheEntry),
		ttl:     ttl,
	}
}

func (c *metadataCache) get(key string) (any, bool) {
	c.mu.RLock()
	entry, exists := c.entries[key]
	expired := exists && time.Since(entry.timestamp) > c.ttl
	if exists && !expired {
		value := entry.value
		c.mu.RUnlock()
		return value, true
	}
	c.mu.RUnlock()

	if expired {
		c.mu.Lock()
		if entry, ok := c.entries[key]; ok && time.Since(entry.timestamp) > c.ttl {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	return nil, false
}

func (c *metadataCache) put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &metadataCacheEntry{
		value:     value,
		timestamp: time.Now(),
	}
}
