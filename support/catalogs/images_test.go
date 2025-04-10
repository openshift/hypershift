package catalogs

import (
	"context"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/blang/semver"
)

func TestComputeCatalogImages(t *testing.T) {
	tests := []struct {
		name              string
		releaseVersion    semver.Version
		existingImages    []string
		registryOverrides map[string][]string
		expected          map[string]string
	}{
		{
			name:           "All current release images are available",
			releaseVersion: semver.MustParse("4.19.2"),
			existingImages: []string{
				"registry.redhat.io/redhat/certified-operator-index:v4.19",
				"registry.redhat.io/redhat/community-operator-index:v4.19",
				"registry.redhat.io/redhat/redhat-marketplace-index:v4.19",
				"registry.redhat.io/redhat/redhat-operator-index:v4.19",
			},
			expected: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.19",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.19",
				"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.19",
				"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.19",
			},
		},
		{
			name:           "Some catalogs only have previous release images",
			releaseVersion: semver.MustParse("4.19.2"),
			existingImages: []string{
				"registry.redhat.io/redhat/certified-operator-index:v4.19",
				"registry.redhat.io/redhat/community-operator-index:v4.17",
				"registry.redhat.io/redhat/redhat-marketplace-index:v4.19",
				"registry.redhat.io/redhat/redhat-operator-index:v4.18",
			},
			expected: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.19",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.17",
				"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.19",
				"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.18",
			},
		},
		{
			name:           "image overrides are used if present",
			releaseVersion: semver.MustParse("4.19.0"),
			existingImages: []string{
				"example.org/test/certified-operator-index:v4.19",
				"example.org/test/community-operator-index:v4.19",
				"example.org/test/community-operator-index:v4.18",
				"another.example.org/redhat/redhat-marketplace-index:v4.19",
				"another.example.org/redhat/redhat-operator-index:v4.19",
			},
			registryOverrides: map[string][]string{
				"registry.redhat.io/redhat": {
					"example.org/test",
					"another.example.org/redhat",
				},
			},
			expected: map[string]string{
				"certified-operators": "example.org/test/certified-operator-index:v4.19",
				"community-operators": "example.org/test/community-operator-index:v4.19",
				"redhat-marketplace":  "another.example.org/redhat/redhat-marketplace-index:v4.19",
				"redhat-operators":    "another.example.org/redhat/redhat-operator-index:v4.19",
			},
		},
		{
			name:           "previous versions are used for overrides",
			releaseVersion: semver.MustParse("4.19.0"),
			existingImages: []string{
				"example.org/test/certified-operator-index:v4.19",
				"example.org/test/community-operator-index:v4.18",
				"another.example.org/redhat/redhat-marketplace-index:v4.19",
				"another.example.org/redhat/redhat-operator-index:v4.17",
			},
			registryOverrides: map[string][]string{
				"registry.redhat.io/redhat": {
					"example.org/test",
					"another.example.org/redhat",
				},
			},
			expected: map[string]string{
				"certified-operators": "example.org/test/certified-operator-index:v4.19",
				"community-operators": "example.org/test/community-operator-index:v4.18",
				"redhat-marketplace":  "another.example.org/redhat/redhat-marketplace-index:v4.19",
				"redhat-operators":    "another.example.org/redhat/redhat-operator-index:v4.17",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := computeCatalogImages(func() (*semver.Version, error) {
				return &tc.releaseVersion, nil
			}, func(image string) (bool, error) {
				return slices.Contains(tc.existingImages, image), nil
			}, tc.registryOverrides)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestImagesCacheGetImages(t *testing.T) {
	tests := []struct {
		name      string
		cache     *imagesCache
		inputHash string
		expected  map[string]string
	}{
		{
			name:      "cache empty",
			cache:     &imagesCache{},
			inputHash: "1234",
			expected:  nil,
		},
		{
			name: "valid entry",
			cache: &imagesCache{
				timeStamp: time.Now(),
				hash:      "4567",
				images: map[string]string{
					"foo":  "bar",
					"foo1": "bar1",
				},
			},
			inputHash: "4567",
			expected: map[string]string{
				"foo":  "bar",
				"foo1": "bar1",
			},
		},
		{
			name: "hash doesn't match",
			cache: &imagesCache{
				timeStamp: time.Now(),
				hash:      "4567",
				images: map[string]string{
					"foo":  "bar",
					"foo1": "bar1",
				},
			},
			inputHash: "1234",
			expected:  nil,
		},
		{
			name: "cache expired",
			cache: &imagesCache{
				timeStamp: time.Now().Add(-30 * time.Minute),
				hash:      "4567",
				images: map[string]string{
					"foo":  "bar",
					"foo1": "bar1",
				},
			},
			inputHash: "4567",
			expected:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := tc.cache.getImages(tc.inputHash)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestImagesCacheSetImages(t *testing.T) {
	g := NewGomegaWithT(t)
	images := map[string]string{
		"foo":  "bar",
		"foo1": "bar1",
	}
	c := &imagesCache{}
	c.setImages(images, "12345")
	result := c.getImages("12345")
	g.Expect(result).To(Equal(images))
	result = c.getImages("45678")
	g.Expect(result).To(BeNil())
}

func TestGetCatalogImagesWithCache(t *testing.T) {
	cacheKeyFn := func() any { return "12345" }
	alternateCacheKeyFn := func() any { return "7890" }
	releaseVersioFn := func() (*semver.Version, error) {
		version := semver.MustParse("4.19.0")
		return &version, nil
	}
	imageExistsFn := func(img string) (bool, error) {
		return true, nil
	}

	only417ImgsExist := func(img string) (bool, error) {
		imgs := []string{
			"registry.redhat.io/redhat/certified-operator-index:v4.17",
			"registry.redhat.io/redhat/community-operator-index:v4.17",
			"registry.redhat.io/redhat/redhat-marketplace-index:v4.17",
			"registry.redhat.io/redhat/redhat-operator-index:v4.17",
		}
		return slices.Contains(imgs, img), nil
	}

	g := NewGomegaWithT(t)

	// First call should not use the cache
	result, err := getCatalogImagesWithCache(cacheKeyFn, releaseVersioFn, imageExistsFn, nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(
		map[string]string{
			"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.19",
			"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.19",
			"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.19",
			"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.19",
		},
	))

	// Next call should use the cache, even if we pass an alternate imageExistsFn
	result, err = getCatalogImagesWithCache(cacheKeyFn, releaseVersioFn, only417ImgsExist, nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(
		map[string]string{
			"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.19",
			"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.19",
			"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.19",
			"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.19",
		},
	))

	// If we change the cache key (such as different release), then the image lookup function should
	// be called again
	result, err = getCatalogImagesWithCache(alternateCacheKeyFn, releaseVersioFn, only417ImgsExist, nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(
		map[string]string{
			"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.17",
			"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.17",
			"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.17",
			"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.17",
		},
	))
}

func TestImageLookupCacheKeyFn(t *testing.T) {
	g := NewGomegaWithT(t)

	hc := &hyperv1.HostedControlPlane{}
	hc.Spec.ReleaseImage = "registry.redhat.io/release:example"
	pullSecret := []byte("12345")
	registryOverrides := map[string][]string{
		"test": {"one", "two"},
	}

	// Test that hashes for the same input are the same
	fn1 := imageLookupCacheKeyFn(hc, pullSecret, registryOverrides)
	fn2 := imageLookupCacheKeyFn(hc, pullSecret, registryOverrides)
	hash1 := util.HashSimple(fn1())
	hash2 := util.HashSimple(fn2())
	g.Expect(hash1).To(Equal(hash2))

	// Test that if we change part of the key, the hashes will defer
	hc.Spec.ReleaseImage = hc.Spec.ReleaseImage + "v2"
	fn3 := imageLookupCacheKeyFn(hc, pullSecret, registryOverrides)
	hash3 := util.HashSimple(fn3())
	g.Expect(hash3).ToNot(Equal(hash1))

	registryOverrides = map[string][]string{
		"test": {"one", "two", "three"},
	}
	fn4 := imageLookupCacheKeyFn(hc, pullSecret, registryOverrides)
	hash4 := util.HashSimple(fn4())
	g.Expect(hash4).ToNot(Equal(hash3))
}

func TestImageExistsFnGuestCluster(t *testing.T) {
	g := NewGomegaWithT(t)

	ctx := context.Background()
	pullSecret := []byte("12345")
	fakeMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result:   &dockerv1client.DockerImageConfig{},
		Manifest: fakeimagemetadataprovider.FakeManifest{},
	}

	// Test to ignore that images don't need to exist for "guest" clusters.
	fn1 := imageExistsFn(ctx, &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{OLMCatalogPlacement: "guest"}}, pullSecret, fakeMetadataProvider)
	exists, err := fn1("something")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(exists).To(Equal(true))

	// Test to ensure we fail if not a guest cluster and can't find image.
	fn2 := imageExistsFn(ctx, &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{OLMCatalogPlacement: "management"}}, pullSecret, fakeMetadataProvider)
	exists, err = fn2("something")
	g.Expect(err).To(HaveOccurred())
	g.Expect(exists).To(Equal(false))
}
