package imageresolution

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestResolveForDirectFetch(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name            string
		ref             string
		overrides       map[string]string
		mirrors         map[string][]string
		mirrorAvailable map[string]bool
		expected        string
	}{
		{
			name:     "When no overrides configured, it should return original ref",
			ref:      "quay.io/openshift/ocp:4.17",
			expected: "quay.io/openshift/ocp:4.17",
		},
		{
			name: "When CLI override matches registry prefix, it should replace it",
			ref:  "quay.io/openshift/ocp:4.17",
			overrides: map[string]string{
				"quay.io": "mirror.azurecr.io",
			},
			expected: "mirror.azurecr.io/openshift/ocp:4.17",
		},
		{
			name: "When CLI override does not match, it should return original",
			ref:  "gcr.io/openshift/ocp:4.17",
			overrides: map[string]string{
				"quay.io": "mirror.azurecr.io",
			},
			expected: "gcr.io/openshift/ocp:4.17",
		},
		{
			name: "When ICSP mirror is available, it should use mirror",
			ref:  "quay.io/openshift/kube-apiserver@sha256:abc123",
			mirrors: map[string][]string{
				"quay.io/openshift": {"mirror1.io/openshift"},
			},
			mirrorAvailable: map[string]bool{
				"mirror1.io": true,
			},
			expected: "mirror1.io/openshift/kube-apiserver@sha256:abc123",
		},
		{
			name: "When first ICSP mirror is down and second is up, it should use second",
			ref:  "quay.io/openshift/kube-apiserver@sha256:abc123",
			mirrors: map[string][]string{
				"quay.io/openshift": {"mirror1.io/openshift", "mirror2.io/openshift"},
			},
			mirrorAvailable: map[string]bool{
				"mirror1.io": false,
				"mirror2.io": true,
			},
			expected: "mirror2.io/openshift/kube-apiserver@sha256:abc123",
		},
		{
			name: "When all ICSP mirrors are down, it should fall back to original",
			ref:  "quay.io/openshift/kube-apiserver@sha256:abc123",
			mirrors: map[string][]string{
				"quay.io/openshift": {"mirror1.io/openshift"},
			},
			mirrorAvailable: map[string]bool{
				"mirror1.io": false,
			},
			expected: "quay.io/openshift/kube-apiserver@sha256:abc123",
		},
		{
			name: "When CLI override and ICSP both apply, CLI is applied first then ICSP on result",
			ref:  "quay.io/openshift/ocp:4.17",
			overrides: map[string]string{
				"quay.io": "intermediate.io",
			},
			mirrors: map[string][]string{
				"intermediate.io/openshift": {"final.io/openshift"},
			},
			mirrorAvailable: map[string]bool{
				"final.io": true,
			},
			expected: "final.io/openshift/ocp:4.17",
		},
		{
			name: "When digest is present, it should be preserved after override",
			ref:  "quay.io/openshift/foo@sha256:deadbeef",
			overrides: map[string]string{
				"quay.io": "mirror.io",
			},
			expected: "mirror.io/openshift/foo@sha256:deadbeef",
		},
		{
			name: "When tag is present, it should be preserved after override",
			ref:  "quay.io/openshift/foo:v1.2.3",
			overrides: map[string]string{
				"quay.io": "mirror.io",
			},
			expected: "mirror.io/openshift/foo:v1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			checker := &fakeMirrorChecker{available: tt.mirrorAvailable}
			cfg := ResolverConfig{
				RegistryOverrides:    tt.overrides,
				ImageRegistryMirrors: tt.mirrors,
			}
			resolver := newImageResolver(
				newStaticConfigSource(cfg),
				checker,
				newMirrorAvailabilityCache(0),
			)

			result, err := resolver.resolveForDirectFetch(ctx, tt.ref)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestResolveForDirectFetch_ConfigSourceError(t *testing.T) {
	t.Run("When config source returns error, resolveForDirectFetch should propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			&failingConfigSource{},
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)

		result, err := resolver.resolveForDirectFetch(t.Context(), "quay.io/openshift/ocp:4.17")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(ContainSubstring("getting resolver config")))
		g.Expect(err).To(MatchError(ContainSubstring("config source unavailable")))
		g.Expect(result).To(Equal("quay.io/openshift/ocp:4.17"), "should return original ref on error")
	})
}

func TestResolveForPodSpec(t *testing.T) {
	ctx := context.Background()

	t.Run("When CLI override matches, it should apply override", func(t *testing.T) {
		g := NewWithT(t)

		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{
				"quay.io": "mirror.azurecr.io",
			},
			ImageRegistryMirrors: map[string][]string{
				"mirror.azurecr.io/openshift": {"icsp-mirror.io/openshift"},
			},
		}
		checker := &fakeMirrorChecker{available: map[string]bool{"icsp-mirror.io": true}}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			checker,
			newMirrorAvailabilityCache(0),
		)

		result, err := resolver.resolveForPodSpec(ctx, "quay.io/openshift/kas@sha256:abc")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal("mirror.azurecr.io/openshift/kas@sha256:abc"))
	})

	t.Run("When ICSP mirrors are configured, it should NOT apply them", func(t *testing.T) {
		g := NewWithT(t)

		cfg := ResolverConfig{
			ImageRegistryMirrors: map[string][]string{
				"quay.io/openshift": {"mirror1.io/openshift"},
			},
		}
		checker := &fakeMirrorChecker{available: map[string]bool{"mirror1.io": true}}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			checker,
			newMirrorAvailabilityCache(0),
		)

		result, err := resolver.resolveForPodSpec(ctx, "quay.io/openshift/kas@sha256:abc")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal("quay.io/openshift/kas@sha256:abc"))
	})
}

func TestResolveForPodSpec_ConfigSourceError(t *testing.T) {
	t.Run("When config source returns error, resolveForPodSpec should propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			&failingConfigSource{},
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)

		result, err := resolver.resolveForPodSpec(t.Context(), "quay.io/openshift/kas@sha256:abc")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(ContainSubstring("getting resolver config")))
		g.Expect(err).To(MatchError(ContainSubstring("config source unavailable")))
		g.Expect(result).To(Equal("quay.io/openshift/kas@sha256:abc"), "should return original ref on error")
	})
}

func TestSortedKeysByLength(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []string
	}{
		{
			name:     "When keys have different lengths, it should sort longest first",
			input:    map[string]string{"a": "1", "ab": "2", "abc": "3"},
			expected: []string{"abc", "ab", "a"},
		},
		{
			name:     "When keys have the same length, it should sort alphabetically",
			input:    map[string]string{"bbb": "1", "aaa": "2", "ccc": "3"},
			expected: []string{"aaa", "bbb", "ccc"},
		},
		{
			name:     "When keys have mixed lengths, longer keys should come before shorter ones",
			input:    map[string]string{"quay.io/openshift": "x", "quay.io": "y", "quay.io/openshift/release": "z"},
			expected: []string{"quay.io/openshift/release", "quay.io/openshift", "quay.io"},
		},
		{
			name:     "When map is empty, it should return empty slice",
			input:    map[string]string{},
			expected: []string{},
		},
		{
			name:     "When single key exists, it should return that key",
			input:    map[string]string{"only": "1"},
			expected: []string{"only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := sortedKeysByLength(tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestIsMirrorAvailable_CacheHit(t *testing.T) {
	t.Run("When calling isMirrorAvailable twice, the second call should use the cached result", func(t *testing.T) {
		g := NewWithT(t)

		callCount := 0
		checker := &countingMirrorChecker{
			available: map[string]bool{"cached-mirror.io": true},
			callCount: &callCount,
		}
		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			checker,
			newMirrorAvailabilityCache(time.Hour),
		)

		ctx := context.Background()

		// First call should invoke the checker.
		result1 := resolver.isMirrorAvailable(ctx, "cached-mirror.io")
		g.Expect(result1).To(BeTrue())
		g.Expect(callCount).To(Equal(1))

		// Second call should hit the cache and not invoke the checker again.
		result2 := resolver.isMirrorAvailable(ctx, "cached-mirror.io")
		g.Expect(result2).To(BeTrue())
		g.Expect(callCount).To(Equal(1))
	})
}

type countingMirrorChecker struct {
	available map[string]bool
	callCount *int
}

func (c *countingMirrorChecker) isAvailable(_ context.Context, mirror string) bool {
	*c.callCount++
	return c.available[mirror]
}
