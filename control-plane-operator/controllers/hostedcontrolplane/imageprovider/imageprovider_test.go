package imageprovider

import (
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewFromImages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		images map[string]string
	}{
		{
			name: "When NewFromImages is called with a map, it should create a provider with those images",
			images: map[string]string{
				"component-a": "registry.example.com/component-a:latest",
				"component-b": "registry.example.com/component-b:v1.0",
			},
		},
		{
			name:   "When NewFromImages is called with an empty map, it should create a provider with no images",
			images: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := NewFromImages(tt.images)

			g.Expect(provider).ToNot(BeNil())
			g.Expect(provider.ComponentImages()).To(Equal(tt.images))
			g.Expect(provider.GetMissingImages()).To(BeEmpty())
		})
	}
}

func TestGetImage(t *testing.T) {
	t.Parallel()

	t.Run("When GetImage is called with an existing key, it should return the image", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"etcd": "registry.example.com/etcd:latest",
		})

		image := provider.GetImage("etcd")

		g.Expect(image).To(Equal("registry.example.com/etcd:latest"))
		g.Expect(provider.GetMissingImages()).To(BeEmpty())
	})

	t.Run("When GetImage is called with a missing key, it should return empty string and track the missing image", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{})

		image := provider.GetImage("nonexistent")

		g.Expect(image).To(BeEmpty())
		g.Expect(provider.GetMissingImages()).To(ConsistOf("nonexistent"))
	})

	t.Run("When GetImage is called with an empty string value, it should track the key as missing", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"empty-image": "",
		})

		image := provider.GetImage("empty-image")

		g.Expect(image).To(BeEmpty())
		g.Expect(provider.GetMissingImages()).To(ConsistOf("empty-image"))
	})

	t.Run("When GetImage is called multiple times with missing keys, it should track all missing images", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"exists": "registry.example.com/exists:latest",
		})

		provider.GetImage("missing-a")
		provider.GetImage("missing-b")
		provider.GetImage("exists")

		g.Expect(provider.GetMissingImages()).To(ConsistOf("missing-a", "missing-b"))
	})
}

func TestImageExist(t *testing.T) {
	t.Parallel()

	t.Run("When ImageExist is called with an existing key, it should return the image and true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"kube-apiserver": "registry.example.com/kube-apiserver:v1.30",
		})

		image, exists := provider.ImageExist("kube-apiserver")

		g.Expect(exists).To(BeTrue())
		g.Expect(image).To(Equal("registry.example.com/kube-apiserver:v1.30"))
	})

	t.Run("When ImageExist is called with a missing key, it should return empty string and false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{})

		image, exists := provider.ImageExist("nonexistent")

		g.Expect(exists).To(BeFalse())
		g.Expect(image).To(BeEmpty())
	})
}

func TestGetMissingImages(t *testing.T) {
	t.Parallel()

	t.Run("When no missing images exist, it should return an empty slice", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"component": "registry.example.com/component:latest",
		})

		g.Expect(provider.GetMissingImages()).To(BeEmpty())
	})

	t.Run("When GetImage has been called with missing keys, it should return those keys", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{})

		provider.GetImage("alpha")
		provider.GetImage("beta")

		g.Expect(provider.GetMissingImages()).To(ConsistOf("alpha", "beta"))
	})
}

func TestComponentImages(t *testing.T) {
	t.Parallel()

	t.Run("When ComponentImages is called, it should return the images map", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		images := map[string]string{
			"component-a": "registry.example.com/component-a:latest",
			"component-b": "registry.example.com/component-b:v2.0",
		}
		provider := NewFromImages(images)

		result := provider.ComponentImages()

		g.Expect(result).To(Equal(images))
	})
}

func newTestReleaseImage(images map[string]string) *releaseinfo.ReleaseImage {
	tags := make([]imageapi.TagReference, 0, len(images))
	for name, image := range images {
		tags = append(tags, imageapi.TagReference{
			Name: name,
			From: &corev1.ObjectReference{Name: image},
		})
	}
	return &releaseinfo.ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.20.0"},
			Spec:       imageapi.ImageStreamSpec{Tags: tags},
		},
	}
}

func TestNewWithRegistryOverrides(t *testing.T) {
	t.Parallel()

	t.Run("When overrides match, component images should be remapped", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := newTestReleaseImage(map[string]string{
			"availability-prober": "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator-4-20:latest",
			"kube-apiserver":      "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
			"etcd":                "registry.access.redhat.com/rhel8/etcd:latest",
		})
		overrides := map[string]string{
			"quay.io": "mirror.example.com/quay-cache",
		}

		provider := NewWithRegistryOverrides(releaseImage, overrides)

		g.Expect(provider.GetImage("availability-prober")).To(Equal(
			"mirror.example.com/quay-cache/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator-4-20:latest"))
		g.Expect(provider.GetImage("kube-apiserver")).To(Equal(
			"mirror.example.com/quay-cache/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123"))
		g.Expect(provider.GetImage("etcd")).To(Equal(
			"registry.access.redhat.com/rhel8/etcd:latest"))
	})

	t.Run("When no overrides provided, images should be unchanged", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := newTestReleaseImage(map[string]string{
			"availability-prober": "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/cpo:latest",
		})

		provider := NewWithRegistryOverrides(releaseImage, nil)

		g.Expect(provider.GetImage("availability-prober")).To(Equal(
			"quay.io/redhat-user-workloads/crt-redhat-acm-tenant/cpo:latest"))
	})

	t.Run("When overrides don't match any image, images should be unchanged", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := newTestReleaseImage(map[string]string{
			"etcd": "registry.access.redhat.com/rhel8/etcd:latest",
		})
		overrides := map[string]string{
			"quay.io": "mirror.example.com",
		}

		provider := NewWithRegistryOverrides(releaseImage, overrides)

		g.Expect(provider.GetImage("etcd")).To(Equal(
			"registry.access.redhat.com/rhel8/etcd:latest"))
	})

	t.Run("When override prefix matches subdomain, it should not apply", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := newTestReleaseImage(map[string]string{
			"component": "quay.io.example.com/namespace/image:tag",
		})
		overrides := map[string]string{
			"quay.io": "mirror.example.com",
		}

		provider := NewWithRegistryOverrides(releaseImage, overrides)

		g.Expect(provider.GetImage("component")).To(Equal(
			"quay.io.example.com/namespace/image:tag"))
	})

	t.Run("When multiple overrides exist, only the matching one should apply", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := newTestReleaseImage(map[string]string{
			"availability-prober": "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/cpo:latest",
			"etcd":                "gcr.io/etcd-development/etcd:v3.5",
		})
		overrides := map[string]string{
			"quay.io": "acr.example.com/quay-cache",
			"gcr.io":  "acr.example.com/gcr-cache",
		}

		provider := NewWithRegistryOverrides(releaseImage, overrides)

		g.Expect(provider.GetImage("availability-prober")).To(Equal(
			"acr.example.com/quay-cache/redhat-user-workloads/crt-redhat-acm-tenant/cpo:latest"))
		g.Expect(provider.GetImage("etcd")).To(Equal(
			"acr.example.com/gcr-cache/etcd-development/etcd:v3.5"))
	})
	t.Run("When applied, longest-prefix override wins over a broader one", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		releaseImage := newTestReleaseImage(map[string]string{
			"kube-apiserver": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
		})
		overrides := map[string]string{
			"quay.io":                       "broad.example.com",
			"quay.io/openshift-release-dev": "narrow.example.com/mirror",
		}

		provider := NewWithRegistryOverrides(releaseImage, overrides)

		g.Expect(provider.GetImage("kube-apiserver")).To(Equal(
			"narrow.example.com/mirror/ocp-v4.0-art-dev@sha256:abc123"))
	})

	t.Run("When applied, overrides and release-image maps are not mutated", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		sourceImages := map[string]string{
			"availability-prober": "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/cpo:latest",
			"kube-apiserver":      "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
		}
		sourceImagesSnapshot := maps.Clone(sourceImages)
		releaseImage := newTestReleaseImage(sourceImages)

		overrides := map[string]string{
			"quay.io": "mirror.example.com/quay-cache",
		}
		overridesSnapshot := maps.Clone(overrides)

		_ = NewWithRegistryOverrides(releaseImage, overrides)

		g.Expect(overrides).To(Equal(overridesSnapshot),
			"NewWithRegistryOverrides must not mutate its overrides argument")
		g.Expect(releaseImage.ComponentImages()).To(Equal(sourceImagesSnapshot),
			"NewWithRegistryOverrides must not mutate the source release image's ComponentImages map")
	})

	t.Run("When applied twice, the second application is a no-op (idempotent)", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		overrides := map[string]string{
			"quay.io": "mirror.example.com/quay-cache",
		}
		firstReleaseImage := newTestReleaseImage(map[string]string{
			"availability-prober": "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/cpo:latest",
			"etcd":                "registry.access.redhat.com/rhel8/etcd:latest",
		})

		firstProvider := NewWithRegistryOverrides(firstReleaseImage, overrides)
		firstImages := maps.Clone(firstProvider.ComponentImages())

		secondReleaseImage := newTestReleaseImage(firstImages)
		secondProvider := NewWithRegistryOverrides(secondReleaseImage, overrides)

		g.Expect(secondProvider.ComponentImages()).To(Equal(firstImages),
			"applying the same overrides a second time must not change images already rewritten")
	})
}
