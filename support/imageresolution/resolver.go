package imageresolution

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type mirrorAvailabilityChecker interface {
	isAvailable(ctx context.Context, mirror string) bool
}

type imageResolver struct {
	configSource configSource
	checker      mirrorAvailabilityChecker
	mirrorCache  *mirrorAvailabilityCache
}

func newImageResolver(
	source configSource,
	checker mirrorAvailabilityChecker,
	mirrorCache *mirrorAvailabilityCache,
) *imageResolver {
	return &imageResolver{
		configSource: source,
		checker:      checker,
		mirrorCache:  mirrorCache,
	}
}

// resolveForDirectFetch applies CLI overrides then ICSP/IDMS mirrors.
// Used when the operator needs to fetch an image from a registry (HTTP call).
func (r *imageResolver) resolveForDirectFetch(ctx context.Context, ref string) (string, error) {
	cfg, err := r.configSource.current(ctx)
	if err != nil {
		return ref, fmt.Errorf("getting resolver config: %w", err)
	}

	resolved := applyCLIOverrides(ref, cfg.RegistryOverrides)
	resolved = r.applyMirrors(ctx, resolved, cfg.ImageRegistryMirrors)

	return resolved, nil
}

// resolveForPodSpec applies CLI overrides only — no ICSP/IDMS mirrors.
// Used when writing image refs into pod specs on the management cluster.
func (r *imageResolver) resolveForPodSpec(ctx context.Context, ref string) (string, error) {
	cfg, err := r.configSource.current(ctx)
	if err != nil {
		return ref, fmt.Errorf("getting resolver config: %w", err)
	}

	return applyCLIOverrides(ref, cfg.RegistryOverrides), nil
}

func applyCLIOverrides(ref string, overrides map[string]string) string {
	for _, src := range sortedKeysByLength(overrides) {
		dst := overrides[src]
		if ref == src || strings.HasPrefix(ref, src+"/") {
			return strings.Replace(ref, src, dst, 1)
		}
	}
	return ref
}

func (r *imageResolver) applyMirrors(
	ctx context.Context,
	ref string,
	mirrors map[string][]string,
) string {
	if len(mirrors) == 0 {
		return ref
	}

	for _, source := range sortedKeysByLength(mirrors) {
		destinations := mirrors[source]
		if ref != source && !strings.HasPrefix(ref, source+"/") {
			continue
		}
		for _, dest := range destinations {
			candidate := strings.Replace(ref, source, dest, 1)
			registry := strings.SplitN(dest, "/", 2)[0]
			if r.isMirrorAvailable(ctx, registry) {
				return candidate
			}
		}
	}
	return ref
}

// sortedKeysByLength returns map keys sorted by descending length, then alphabetically.
// Longest-prefix-first ensures more specific overrides take precedence.
func sortedKeysByLength[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	return keys
}

func (r *imageResolver) isMirrorAvailable(ctx context.Context, registry string) bool {
	if avail, ok := r.mirrorCache.get(registry); ok {
		return avail
	}

	avail := r.checker.isAvailable(ctx, registry)
	r.mirrorCache.set(registry, avail)
	return avail
}
