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
// valid image-reference separator ("/" for path components, "@" for digests,
// or ":" for tags). This prevents accidental substring matches (e.g. an
// override for "quay.io" must not match "quay.io.example.com/foo") while
// correctly handling repository-level overrides against digest references
// (e.g. "quay.io/org/repo" must match "quay.io/org/repo@sha256:abc").
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
		if !matchesPrefix(image, source) {
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

// matchesPrefix reports whether image is exactly source, or source is a prefix
// of image followed by a valid separator. Valid separators are "/" (path) and
// "@" (digest), always. ":" is accepted only when source contains a "/" (i.e.
// includes a path component), where it denotes a tag boundary. Without a "/"
// the source is a bare hostname and ":" would be a port separator, which must
// not match (e.g. override "quay.io" must not match "quay.io:5000/foo").
func matchesPrefix(image, source string) bool {
	if image == source {
		return true
	}
	if !strings.HasPrefix(image, source) {
		return false
	}
	sep := image[len(source)]
	if sep == '/' || sep == '@' {
		return true
	}
	return sep == ':' && strings.ContainsRune(source, '/')
}
