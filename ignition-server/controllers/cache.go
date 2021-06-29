package controllers

import (
	"sync"
	"time"
)

// ExpiringCache enables a cache of pairs "token: payload".
// Any pair in the cache is expired once entry.expiry time is above the cache ttl.
// The expiry time is renewed for an existing value on every Get operation.
// Garbage collection of expired values happens on every Get operation.
type ExpiringCache struct {
	cache map[string]*entry
	ttl   time.Duration
	sync.RWMutex
}

type entry struct {
	value  []byte
	expiry time.Time
}

func (c *ExpiringCache) Get(key string) (value []byte, ok bool) {
	c.RLock()
	defer c.RUnlock()

	c.garbageCollect()

	result, ok := c.cache[key]
	if !ok {
		return nil, false
	}

	// Renew expiring time everytime time we Get.
	result.expiry = time.Now().Add(c.ttl)
	return result.value, ok
}

func (c *ExpiringCache) Set(key string, value []byte) {
	c.Lock()
	defer c.Unlock()

	// Renew expiring time everytime time we Set.
	c.cache[key] = &entry{
		value:  value,
		expiry: time.Now().Add(c.ttl),
	}
}

func (c *ExpiringCache) Delete(key string) {
	c.Lock()
	defer c.Unlock()
	delete(c.cache, key)
}

func (c *ExpiringCache) garbageCollect() {
	for key, entry := range c.cache {
		if time.Now().After(entry.expiry) {
			c.Delete(key)
		}
	}
}
