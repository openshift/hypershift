package controllers

import (
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	PayloadCacheSizeTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ign_server_payload_cache_total",
	})
)

func init() {
	metrics.Registry.MustRegister(
		PayloadCacheSizeTotal,
	)
}

// ExpiringCache enables a cache of pairs "token: payload".
// Any pair in the cache is expired once entry.expiry time is above the cache ttl.
// The expiry time is renewed for an existing value on every Get operation.
// Garbage collection of expired values happens on every Get operation.
type ExpiringCache struct {
	cache map[string]*entry
	ttl   time.Duration
	sync.RWMutex
}

type CacheValue struct {
	Payload    []byte
	SecretName string
}

type entry struct {
	value  CacheValue
	expiry time.Time
}

func (c *ExpiringCache) Get(key string) (value CacheValue, ok bool) {
	c.garbageCollect()

	c.RLock()
	defer c.RUnlock()

	result, ok := c.cache[key]
	if !ok {
		return CacheValue{}, false
	}

	return result.value, ok
}

func (c *ExpiringCache) Set(key string, value CacheValue) {
	c.Lock()
	defer c.Unlock()

	// Renew expiring time every time time we Set.
	c.cache[key] = &entry{
		value:  value,
		expiry: time.Now().Add(c.ttl),
	}
	PayloadCacheSizeTotal.Inc()
}

func (c *ExpiringCache) Delete(key string) {
	c.Lock()
	defer c.Unlock()
	delete(c.cache, key)
	PayloadCacheSizeTotal.Dec()
}

func (c *ExpiringCache) Keys() []string {
	c.RLock()
	defer c.RUnlock()

	var keys []string
	for k := range c.cache {
		keys = append(keys, k)
	}
	return keys
}

func (c *ExpiringCache) garbageCollect() {
	for key, entry := range c.cache {
		if time.Now().After(entry.expiry) {
			c.Delete(key)
		}
	}
}
