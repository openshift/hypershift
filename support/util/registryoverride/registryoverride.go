// Package registryoverride applies registry-prefix overrides to image
// references with strict, deterministic matching semantics. It is intentionally
// a leaf package with no project-internal imports so it can be used both from
// support/releaseinfo and from sub-packages that depend on it.
package registryoverride

import "strings"

// Replace remaps an image reference using a set of registry-prefix overrides.
// The overrides map keys are source registry prefixes and values are
// replacement prefixes.
//
// Matching is strict: an override applies only when the image reference is
// exactly equal to the source key or starts with the source key followed by a
// "/" separator. This prevents accidental substring matches (e.g. an override
// for "quay.io" must not match "quay.io.example.com/foo").
//
// When several override keys match the same image, the longest key wins. This
// makes the result deterministic regardless of map iteration order and lets
// callers express both broad ("quay.io") and narrow
// ("quay.io/openshift-release-dev") overrides simultaneously.
//
// If no override matches, image is returned unchanged.
func Replace(image string, overrides map[string]string) string {
	if image == "" || len(overrides) == 0 {
		return image
	}

	var bestSource, bestTarget string
	for source, target := range overrides {
		if source == "" {
			continue
		}
		if image != source && !strings.HasPrefix(image, source+"/") {
			continue
		}
		if len(source) > len(bestSource) {
			bestSource, bestTarget = source, target
		}
	}
	if bestSource == "" {
		return image
	}
	return bestTarget + image[len(bestSource):]
}
