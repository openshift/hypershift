package imageresolution

import (
	"context"
	"errors"
)

type fakeMirrorChecker struct {
	available map[string]bool
}

func (f *fakeMirrorChecker) isAvailable(_ context.Context, mirror string) bool {
	if f.available == nil {
		return false
	}
	return f.available[mirror]
}

var errConfigSourceFailed = errors.New("config source unavailable")

type failingConfigSource struct{}

func (f *failingConfigSource) current(_ context.Context) (ResolverConfig, error) {
	return ResolverConfig{}, errConfigSourceFailed
}

// callCountingConfigSource succeeds for the first failAfter calls, then fails.
type callCountingConfigSource struct {
	failAfter int
	calls     int
	config    ResolverConfig
}

func (c *callCountingConfigSource) current(_ context.Context) (ResolverConfig, error) {
	c.calls++
	if c.calls > c.failAfter {
		return ResolverConfig{}, errConfigSourceFailed
	}
	return c.config.Clone(), nil
}
