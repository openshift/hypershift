package imageresolution

import (
	"context"
	"sync"
)

type configSource interface {
	current(ctx context.Context) (ResolverConfig, error)
}

type staticConfigSource struct {
	config ResolverConfig
}

func newStaticConfigSource(cfg ResolverConfig) *staticConfigSource {
	return &staticConfigSource{config: cfg.Clone()}
}

func (s *staticConfigSource) current(_ context.Context) (ResolverConfig, error) {
	return s.config.Clone(), nil
}

type mutableConfigSource struct {
	mu     sync.RWMutex
	config ResolverConfig
}

func newMutableConfigSource(cfg ResolverConfig) *mutableConfigSource {
	return &mutableConfigSource{config: cfg.Clone()}
}

func (m *mutableConfigSource) current(_ context.Context) (ResolverConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Clone(), nil
}

func (m *mutableConfigSource) updateMirrors(mirrors map[string][]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.ImageRegistryMirrors = cloneStringSliceMap(mirrors)
}
