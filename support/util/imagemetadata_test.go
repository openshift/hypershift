package util

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
)

func TestGetRegistryOverrides(t *testing.T) {
	ctx := t.Context()
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		source         string
		mirror         string
		expectedImgRef *reference.DockerImageReference
		expectAnErr    bool
		overrideFound  bool
	}{
		{
			name: "if failed to parse source image",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source:         "",
			mirror:         "",
			expectedImgRef: nil,
			expectAnErr:    true,
			overrideFound:  false,
		},
		{
			name: "if registry override coincidence not found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			mirror: "myregistry.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr:   false,
			overrideFound: false,
		},
		{
			name: "if registry override coincidence is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			mirror: "myregistry.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr:   false,
			overrideFound: true,
		},
		{
			name: "if registry override partial coincidence is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				Tag:       "multiarch",
			},
			source: "quay.io/mce",
			mirror: "quay.io/openshifttest",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				Tag:       "multiarch",
			},
			expectAnErr:   false,
			overrideFound: true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := GetRegistryOverrides(ctx, tc.ref, tc.source, tc.mirror)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
		})
	}
}

func TestSeekOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		overrides      map[string][]string
		imageRef       reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
	}{
		{
			name:      "if no overrides are provided, and multi mirrors",
			overrides: map[string][]string{},
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
		},
		{
			name:      "if registry override exact coincidence is found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				Tag:       "4.15.0-rc.0-multi",
			},
		},
		{
			name:      "if registry override partial coincidence is found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				Tag:       "multiarch",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				Tag:       "multiarch",
			},
		},
		{
			name:      "if registry override coincidence is not found",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "testimage",
				Namespace: "test-namespace",
				Tag:       "latest",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "testimage",
				Namespace: "test-namespace",
				Tag:       "latest",
			},
		},
		{
			name:      "if failed to find registry override",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "cnv-image",
				Namespace: "cnv",
				Tag:       "latest",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "cnv-image",
				Namespace: "cnv",
				Tag:       "latest",
			},
		},
		{
			name:      "if registry override exact coincidence is found, and using ID",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build01.ci.openshift.org",
				Name:      "release",
				Namespace: "ci-op-p2mqdwjp",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
		},
		{
			//busybox@sha256:c5439d7db88ab5423999530349d327b04279ad3161d7596d2126dfb5b02bfd1f
			name:      "if registry override partial coincidence is found, and using ID",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				ID:        "sha256:c5439d7db88ab5423999530349d327b04279ad3161d7596d2126dfb5b02bfd1f",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				ID:        "sha256:c5439d7db88ab5423999530349d327b04279ad3161d7596d2126dfb5b02bfd1f",
			},
		},
		{
			name:      "if only the root registry is provided",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
		},
		{
			name:      "if only the root registry is provided and multiple mirrors are provided",
			overrides: fakeOverrides(),
			imageRef: reference.DockerImageReference{
				Registry:  "registry.build03.ci.openshift.org",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshifttest",
				ID:        "sha256:b272d47dded73ec8d9eb01a8e39cd62a453d2799c1785ecd538aa8cd15693bf0",
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			g := NewGomegaWithT(t)
			pullSecret, err := os.ReadFile("../../hack/dev/fakePullSecret.json")
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			imgRef := SeekOverride(ctx, tc.overrides, tc.imageRef, pullSecret)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef), fmt.Sprintf("Expected image reference to be equal to: %v, \nbut got: %v", tc.expectedImgRef, imgRef))
		})
	}
}

func fakeOverrides() map[string][]string {
	return map[string][]string{
		"quay.io/openshift-release-dev/ocp-release": {
			"myregistry1.io/openshift-release-dev/ocp-release",
			"quay.io/openshifttest/ocp-release",
		},
		"quay.io/mce": {
			"quay.io/openshifttest",
		},
		"registry.build01.ci.openshift.org/ci-op-p2mqdwjp/release": {
			"quay.io/openshifttest/ocp-release",
		},
		"registry.ci.openshift.org/ocp/4.18-2025-01-04-031500": {
			"virthost.ostest.test.metalkube.org:5000/localimages/local-release-image",
		},
		"registry.build02.ci.openshift.org": {
			"quay.io",
		},
		"registry.build03.ci.openshift.org": {
			"myregistry1.io",
			"myregistry2.io",
			"quay.io",
		},
		"quay.io/prometheus": {
			"brew.registry.redhat.io/prometheus",
		},
	}
}

func TestTryOnlyNamespaceOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if namespace override is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry: "quay.io",
				Name:     "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
				Name:     "openshift-release-dev",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if namespace override is not found - namespace not empty",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Namespace: "test",
				Name:      "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if namespace override is not found - name mismatch",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "different-namespace",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryOnlyNamespaceOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestTryExactCoincidenceOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if exact coincidence override is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry:  "myregistry.io",
				Namespace: "openshift-release-dev",
				Name:      "ocp-release",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if exact coincidence override is not found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "different-name",
				Namespace: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryExactCoincidenceOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			if tc.overrideFound {
				g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			} else {
				g.Expect(imgRef).To(BeNil())
			}
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestTryOnlyRootRegistryOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if root registry override is found",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Name: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "virthost.ostest.test.metalkube.org:5000",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if root registry override is not found - namespace not empty",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Namespace: "test",
				Name:      "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if root registry override is not found - registry not empty",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry: "test",
				Name:     "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if root registry override is not found - name mismatch",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "different-registry",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryOnlyRootRegistryOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			if tc.overrideFound {
				g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			} else {
				g.Expect(imgRef).To(BeNil())
			}
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestMirrorAvailabilityCache(t *testing.T) {
	g := NewGomegaWithT(t)

	// Create a new cache instance for testing
	testCache := &MirrorAvailabilityCache{
		cache: make(map[string]mirrorCacheEntry),
	}

	testURL := "test-mirror.example.com/image:tag"
	testPullSecret := []byte(`{"auths":{"registry.example.com":{"username":"test","password":"secret"}}}`)

	t.Run("cache miss returns false", func(t *testing.T) {
		available, found := testCache.get(testURL, testPullSecret)
		g.Expect(found).To(BeFalse())
		g.Expect(available).To(BeFalse())
	})

	t.Run("cache hit returns stored value", func(t *testing.T) {
		// Set available = true
		testCache.set(testURL, testPullSecret, true)

		available, found := testCache.get(testURL, testPullSecret)
		g.Expect(found).To(BeTrue())
		g.Expect(available).To(BeTrue())
	})

	t.Run("cache stores negative results", func(t *testing.T) {
		testURL2 := "unavailable-mirror.example.com/image:tag"

		// Set available = false
		testCache.set(testURL2, testPullSecret, false)

		available, found := testCache.get(testURL2, testPullSecret)
		g.Expect(found).To(BeTrue())
		g.Expect(available).To(BeFalse())
	})

	t.Run("cache expiration works", func(t *testing.T) {
		testURL3 := "expired-mirror.example.com/image:tag"

		// Set with false (1 minute TTL)
		testCache.set(testURL3, testPullSecret, false)

		// Manually expire the entry
		cacheKey3 := generateCacheKey(testURL3, testPullSecret)
		testCache.mutex.Lock()
		if entry, exists := testCache.cache[cacheKey3]; exists {
			entry.timestamp = time.Now().Add(-2 * time.Minute) // 2 minutes ago
			testCache.cache[cacheKey3] = entry
		}
		testCache.mutex.Unlock()

		// Should be cache miss now
		available, found := testCache.get(testURL3, testPullSecret)
		g.Expect(found).To(BeFalse())
		g.Expect(available).To(BeFalse())

		// Entry should be cleaned up
		testCache.mutex.RLock()
		_, exists := testCache.cache[cacheKey3]
		testCache.mutex.RUnlock()
		g.Expect(exists).To(BeFalse())
	})

	t.Run("TTL is different for available vs unavailable", func(t *testing.T) {
		availableURL := "available-ttl.example.com/image:tag"
		unavailableURL := "unavailable-ttl.example.com/image:tag"

		testCache.set(availableURL, testPullSecret, true)
		testCache.set(unavailableURL, testPullSecret, false)

		availableCacheKey := generateCacheKey(availableURL, testPullSecret)
		unavailableCacheKey := generateCacheKey(unavailableURL, testPullSecret)

		testCache.mutex.RLock()
		availableEntry := testCache.cache[availableCacheKey]
		unavailableEntry := testCache.cache[unavailableCacheKey]
		testCache.mutex.RUnlock()

		g.Expect(availableEntry.ttl).To(Equal(5 * time.Minute))
		g.Expect(unavailableEntry.ttl).To(Equal(1 * time.Minute))
	})

	t.Run("concurrent access is thread safe", func(t *testing.T) {
		concurrentURL := "concurrent-test.example.com/image:tag"

		// Test concurrent reads and writes
		done := make(chan bool, 10)

		// Start multiple goroutines that read and write
		for i := 0; i < 5; i++ {
			go func() {
				testCache.set(concurrentURL, testPullSecret, true)
				testCache.get(concurrentURL, testPullSecret)
				done <- true
			}()
		}

		for i := 0; i < 5; i++ {
			go func() {
				testCache.set(concurrentURL, testPullSecret, false)
				testCache.get(concurrentURL, testPullSecret)
				done <- true
			}()
		}

		// Wait for all goroutines to complete
		for i := 0; i < 10; i++ {
			<-done
		}

		// Should not panic and should have some value
		_, found := testCache.get(concurrentURL, testPullSecret)
		g.Expect(found).To(BeTrue())
	})
}

func TestSeekOverrideWithCache(t *testing.T) {
	g := NewGomegaWithT(t)

	// Reset the global cache for clean test
	mirrorCache.mutex.Lock()
	mirrorCache.cache = make(map[string]mirrorCacheEntry)
	mirrorCache.mutex.Unlock()

	ctx := context.Background()

	t.Run("cache prevents repeated network verification", func(t *testing.T) {
		overrides := map[string][]string{
			"quay.io": {"mirror.example.com"},
		}

		parsedRef := reference.DockerImageReference{
			Registry:  "quay.io",
			Namespace: "test",
			Name:      "image",
			Tag:       "latest",
		}

		// First call will attempt network verification (which will likely fail for our test mirror)
		// and cache the result
		result1 := SeekOverride(ctx, overrides, parsedRef, []byte(`{"auths":{}}`))

		// Second call should use cache and return same result without network call
		result2 := SeekOverride(ctx, overrides, parsedRef, []byte(`{"auths":{}}`))

		g.Expect(result1).To(Equal(result2))

		// Check that result is cached
		mirrorURL := "mirror.example.com/test/image:latest"
		pullSecret := []byte(`{"auths":{}}`)
		_, found := mirrorCache.get(mirrorURL, pullSecret)
		g.Expect(found).To(BeTrue(), "Mirror availability should be cached")
	})

	t.Run("cache respects different mirror URLs", func(t *testing.T) {
		overrides1 := map[string][]string{
			"quay.io": {"mirror1.example.com"},
		}
		overrides2 := map[string][]string{
			"quay.io": {"mirror2.example.com"},
		}

		parsedRef := reference.DockerImageReference{
			Registry:  "quay.io",
			Namespace: "test",
			Name:      "image",
			Tag:       "latest",
		}

		// Test with first mirror
		SeekOverride(ctx, overrides1, parsedRef, []byte(`{"auths":{}}`))

		// Test with second mirror
		SeekOverride(ctx, overrides2, parsedRef, []byte(`{"auths":{}}`))

		// Both mirrors should be cached separately
		pullSecret := []byte(`{"auths":{}}`)
		_, found1 := mirrorCache.get("mirror1.example.com/test/image:latest", pullSecret)
		_, found2 := mirrorCache.get("mirror2.example.com/test/image:latest", pullSecret)

		g.Expect(found1).To(BeTrue(), "First mirror should be cached")
		g.Expect(found2).To(BeTrue(), "Second mirror should be cached")
	})
}

func TestSeekOverrideTimeout(t *testing.T) {
	g := NewGomegaWithT(t)

	// Reset the global cache
	mirrorCache.mutex.Lock()
	mirrorCache.cache = make(map[string]mirrorCacheEntry)
	mirrorCache.mutex.Unlock()

	ctx := context.Background()

	overrides := map[string][]string{
		"quay.io": {"nonexistent-mirror.invalid"},
	}

	parsedRef := reference.DockerImageReference{
		Registry:  "quay.io",
		Namespace: "test",
		Name:      "image",
		Tag:       "latest",
	}

	// This should timeout and fallback to original image
	result := SeekOverride(ctx, overrides, parsedRef, []byte(`{"auths":{}}`))

	// Should return original reference since mirror is invalid
	g.Expect(result.Registry).To(Equal("quay.io"))
	g.Expect(result.Namespace).To(Equal("test"))
	g.Expect(result.Name).To(Equal("image"))
	g.Expect(result.Tag).To(Equal("latest"))

	// Should cache the negative result
	mirrorURL := "nonexistent-mirror.invalid/test/image:latest"
	pullSecret := []byte(`{"auths":{}}`)
	available, found := mirrorCache.get(mirrorURL, pullSecret)
	g.Expect(found).To(BeTrue())
	g.Expect(available).To(BeFalse())
}

func TestCacheCleanupOnExpiration(t *testing.T) {
	g := NewGomegaWithT(t)

	testCache := &MirrorAvailabilityCache{
		cache: make(map[string]mirrorCacheEntry),
	}

	testPullSecret := []byte(`{"auths":{"registry.example.com":{"username":"test","password":"secret"}}}`)

	// Add multiple entries
	testCache.set("url1.example.com/image:tag", testPullSecret, true)
	testCache.set("url2.example.com/image:tag", testPullSecret, false)
	testCache.set("url3.example.com/image:tag", testPullSecret, true)

	// Verify all are cached
	g.Expect(len(testCache.cache)).To(Equal(3))

	// Manually expire some entries
	cacheKey1 := generateCacheKey("url1.example.com/image:tag", testPullSecret)
	cacheKey2 := generateCacheKey("url2.example.com/image:tag", testPullSecret)

	testCache.mutex.Lock()
	for cacheKey, entry := range testCache.cache {
		if cacheKey == cacheKey1 || cacheKey == cacheKey2 {
			entry.timestamp = time.Now().Add(-10 * time.Minute) // Long ago
			testCache.cache[cacheKey] = entry
		}
	}
	testCache.mutex.Unlock()

	// Access expired entries should clean them up
	testCache.get("url1.example.com/image:tag", testPullSecret)
	testCache.get("url2.example.com/image:tag", testPullSecret)

	// Only non-expired entry should remain
	testCache.mutex.RLock()
	remainingEntries := len(testCache.cache)
	testCache.mutex.RUnlock()

	g.Expect(remainingEntries).To(Equal(1))
}
