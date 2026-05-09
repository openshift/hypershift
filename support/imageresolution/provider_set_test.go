package imageresolution

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestProviderSetBuilder_Build(t *testing.T) {
	t.Run("When minimal config, it should build successfully", func(t *testing.T) {
		g := NewWithT(t)
		ps, err := NewProviderSet().Build()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps).ToNot(BeNil())
		g.Expect(ps.Config().IsEmpty()).To(BeTrue())
	})

	t.Run("When ForDataPlane with WithRegistryOverrides, Build should reject", func(t *testing.T) {
		g := NewWithT(t)
		_, err := NewProviderSet().
			ForDataPlane().
			WithRegistryOverrides(map[string]string{"quay.io": "mirror.io"}).
			Build()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("ForDataPlane"))
	})

	t.Run("When ForDataPlane with WithImageRegistryMirrors, it should build successfully", func(t *testing.T) {
		g := NewWithT(t)
		ps, err := NewProviderSet().
			ForDataPlane().
			WithImageRegistryMirrors(map[string][]string{"quay.io": {"mirror.io"}}).
			Build()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps).ToNot(BeNil())
	})

	t.Run("When ForDataPlane with WithMirrorRefresh, Build should reject", func(t *testing.T) {
		g := NewWithT(t)
		_, err := NewProviderSet().
			ForDataPlane().
			WithMirrorRefresh(func(_ context.Context, _ crclient.Client) (map[string][]string, error) {
				return nil, nil
			}).
			Build()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("ForDataPlane"))
	})

	t.Run("When ForDataPlane with WithImageOverrides, Build should reject", func(t *testing.T) {
		g := NewWithT(t)
		_, err := NewProviderSet().
			ForDataPlane().
			WithImageOverrides(map[string]string{"kube-apiserver": "custom:latest"}).
			Build()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("ForDataPlane"))
	})

	t.Run("When ForDataPlane only, it should build successfully", func(t *testing.T) {
		g := NewWithT(t)
		ps, err := NewProviderSet().
			ForDataPlane().
			Build()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps).ToNot(BeNil())
		g.Expect(ps.Config().IsEmpty()).To(BeTrue())
	})

	t.Run("When Config is called, it should return the resolver configuration", func(t *testing.T) {
		g := NewWithT(t)
		overrides := map[string]string{"quay.io": "mirror.io"}
		ps, err := NewProviderSet().
			WithRegistryOverrides(overrides).
			Build()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps.Config().RegistryOverrides).To(Equal(overrides))
	})

	t.Run("When Config is called on zero-value ProviderSet, it should return empty ResolverConfig", func(t *testing.T) {
		g := NewWithT(t)
		ps := &ProviderSet{}
		cfg := ps.Config()
		g.Expect(cfg).To(Equal(ResolverConfig{}))
		g.Expect(cfg.IsEmpty()).To(BeTrue())
	})

	t.Run("When Reconcile mirror refresh returns error, it should wrap and return the error", func(t *testing.T) {
		g := NewWithT(t)

		refreshErr := fmt.Errorf("network timeout")
		ps, err := NewProviderSet().
			WithMirrorRefresh(func(_ context.Context, _ crclient.Client) (map[string][]string, error) {
				return nil, refreshErr
			}).
			WithReleaseFetcher(&fakeReleaseFetcher{releases: map[string]*ReleaseImage{}}).
			WithMetadataFetcher(&fakeMetadataFetcher{configs: map[string]*dockerv1client.DockerImageConfig{}}).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		err = ps.Reconcile(context.Background(), nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("refreshing image registry mirrors"))
		g.Expect(err.Error()).To(ContainSubstring("network timeout"))
	})

	t.Run("When WithMirrorChecker is used, it should use the custom checker", func(t *testing.T) {
		g := NewWithT(t)

		customChecker := &fakeMirrorChecker{
			available: map[string]bool{"custom-mirror.io": true},
		}
		ps, err := NewProviderSet().
			WithImageRegistryMirrors(map[string][]string{
				"quay.io/openshift": {"custom-mirror.io/openshift"},
			}).
			WithMirrorChecker(customChecker).
			WithReleaseFetcher(&fakeReleaseFetcher{
				releases: map[string]*ReleaseImage{
					"custom-mirror.io/openshift/ocp:4.17": {
						ComponentImages: map[string]string{},
					},
				},
			}).
			WithMetadataFetcher(&fakeMetadataFetcher{configs: map[string]*dockerv1client.DockerImageConfig{}}).
			Build()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps).ToNot(BeNil())

		// Verify the custom checker is actually used by doing a release lookup
		// that should go through the mirror.
		release, err := ps.release.Lookup(context.Background(), "quay.io/openshift/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(release).ToNot(BeNil())
	})
}

func TestProviderSet_Lookup(t *testing.T) {
	t.Run("When Lookup succeeds, it should return a releaseinfo.ReleaseImage with ImageStream and StreamMetadata", func(t *testing.T) {
		g := NewWithT(t)

		is := &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: "kube-apiserver",
						From: &corev1.ObjectReference{Name: "quay.io/openshift/kube-apiserver@sha256:abc"},
					},
				},
			},
		}
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"quay.io/ocp:4.17": {
					ComponentImages:   map[string]string{"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:abc"},
					ComponentVersions: map[string]string{"release": "4.17.0"},
					ImageStream:       is,
					StreamMetadata:    &releaseinfo.CoreOSStreamMetadata{Stream: "4.17"},
				},
			},
		}
		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))
		ps := &ProviderSet{release: provider, resolver: resolver}

		result, err := ps.Lookup(context.Background(), "quay.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.ImageStream).ToNot(BeNil())
		g.Expect(result.ImageStream.Name).To(Equal("4.17.0"))
		g.Expect(result.StreamMetadata).ToNot(BeNil())
		g.Expect(result.StreamMetadata.Stream).To(Equal("4.17"))
	})

	t.Run("When the underlying release fetcher returns an error, it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{},
		}
		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))
		ps := &ProviderSet{release: provider, resolver: resolver}

		result, err := ps.Lookup(context.Background(), "quay.io/nonexistent:latest", []byte("secret"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("release not found"))
		g.Expect(result).To(BeNil())
	})

	t.Run("When registry overrides are configured, it should apply overrides to ImageStream tags", func(t *testing.T) {
		g := NewWithT(t)

		is := &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: "kube-apiserver",
						From: &corev1.ObjectReference{Name: "original.registry.io/openshift/kube-apiserver@sha256:abc"},
					},
					{
						Name: "etcd",
						From: &corev1.ObjectReference{Name: "original.registry.io/openshift/etcd@sha256:def"},
					},
				},
			},
		}
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"mirror.registry.io/ocp:4.17": {
					ComponentImages: map[string]string{
						"kube-apiserver": "original.registry.io/openshift/kube-apiserver@sha256:abc",
						"etcd":           "original.registry.io/openshift/etcd@sha256:def",
					},
					ComponentVersions: map[string]string{"release": "4.17.0"},
					ImageStream:       is,
					StreamMetadata:    &releaseinfo.CoreOSStreamMetadata{Stream: "4.17"},
				},
			},
		}
		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{"original.registry.io": "mirror.registry.io"},
		}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))
		ps := &ProviderSet{release: provider, resolver: resolver}

		result, err := ps.Lookup(context.Background(), "original.registry.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())

		componentImages := result.ComponentImages()
		g.Expect(componentImages).To(HaveKey("kube-apiserver"))
		g.Expect(componentImages["kube-apiserver"]).To(Equal("mirror.registry.io/openshift/kube-apiserver@sha256:abc"))
		g.Expect(componentImages).To(HaveKey("etcd"))
		g.Expect(componentImages["etcd"]).To(Equal("mirror.registry.io/openshift/etcd@sha256:def"))
	})

	t.Run("When image overrides are configured, it should replace matching component images", func(t *testing.T) {
		g := NewWithT(t)

		is := &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: "kube-apiserver",
						From: &corev1.ObjectReference{Name: "quay.io/openshift/kube-apiserver@sha256:abc"},
					},
					{
						Name: "etcd",
						From: &corev1.ObjectReference{Name: "quay.io/openshift/etcd@sha256:def"},
					},
				},
			},
		}
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"quay.io/ocp:4.17": {
					ComponentImages: map[string]string{
						"kube-apiserver": "quay.io/openshift/kube-apiserver@sha256:abc",
						"etcd":           "quay.io/openshift/etcd@sha256:def",
					},
					ComponentVersions: map[string]string{"release": "4.17.0"},
					ImageStream:       is,
					StreamMetadata:    &releaseinfo.CoreOSStreamMetadata{Stream: "4.17"},
				},
			},
		}
		imageOverrides := map[string]string{
			"kube-apiserver": "my-custom-registry.io/custom/kube-apiserver:latest",
		}
		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		provider := newReleaseInfoProvider(resolver, fetcher, imageOverrides, newReleaseCache(0))
		ps := &ProviderSet{release: provider, resolver: resolver}

		result, err := ps.Lookup(context.Background(), "quay.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())

		componentImages := result.ComponentImages()
		g.Expect(componentImages["kube-apiserver"]).To(Equal("my-custom-registry.io/custom/kube-apiserver:latest"),
			"overridden component should use the custom image")
		g.Expect(componentImages["etcd"]).To(Equal("quay.io/openshift/etcd@sha256:def"),
			"non-overridden component should keep the original image")
	})
}

func TestProviderSet_GetMirroredReleaseImage(t *testing.T) {
	t.Run("When release was fetched from a mirror, it should return the mirrored pullspec", func(t *testing.T) {
		g := NewWithT(t)

		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{"quay.io": "mirror.io"},
		}
		resolver := newImageResolver(
			newStaticConfigSource(cfg),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"mirror.io/ocp:4.17": {
					ComponentImages: map[string]string{},
				},
			},
		}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))
		ps := &ProviderSet{release: provider, resolver: resolver}

		_, err := ps.Lookup(context.Background(), "quay.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps.GetMirroredReleaseImage()).To(Equal("mirror.io/ocp:4.17"))
	})

	t.Run("When release was not mirrored, it should return empty string", func(t *testing.T) {
		g := NewWithT(t)

		resolver := newImageResolver(
			newStaticConfigSource(ResolverConfig{}),
			&fakeMirrorChecker{},
			newMirrorAvailabilityCache(0),
		)
		fetcher := &fakeReleaseFetcher{
			releases: map[string]*ReleaseImage{
				"quay.io/ocp:4.17": {
					ComponentImages: map[string]string{},
				},
			},
		}
		provider := newReleaseInfoProvider(resolver, fetcher, nil, newReleaseCache(0))
		ps := &ProviderSet{release: provider, resolver: resolver}

		_, err := ps.Lookup(context.Background(), "quay.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ps.GetMirroredReleaseImage()).To(BeEmpty())
	})
}

func TestNoopMirrorChecker(t *testing.T) {
	t.Run("When built without mirrors, noopMirrorChecker should return false", func(t *testing.T) {
		g := NewWithT(t)

		checker := &noopMirrorChecker{}
		g.Expect(checker.isAvailable(context.Background(), "any-registry.io")).To(BeFalse())
		g.Expect(checker.isAvailable(context.Background(), "another-registry.io")).To(BeFalse())
	})
}

func TestProviderSet_WithReleaseProvider(t *testing.T) {
	t.Run("When WithReleaseProvider is used, Lookup should delegate to the injected provider", func(t *testing.T) {
		g := NewWithT(t)

		fakeProvider := &fakereleaseprovider.FakeReleaseProvider{
			Version: "4.17.0",
		}

		ps, err := NewProviderSet().
			WithReleaseProvider(fakeProvider).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		result, err := ps.Lookup(t.Context(), "quay.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).ToNot(BeNil())
		g.Expect(result.ImageStream).ToNot(BeNil())
		g.Expect(result.ImageStream.Name).To(Equal("4.17.0"))
		g.Expect(result.StreamMetadata).ToNot(BeNil())
	})

	t.Run("When WithReleaseProvider is used with image-based versioning, Lookup should return the correct version", func(t *testing.T) {
		g := NewWithT(t)

		fakeProvider := &fakereleaseprovider.FakeReleaseProvider{
			ImageVersion: map[string]string{
				"quay.io/ocp:4.17": "4.17.5",
				"quay.io/ocp:4.18": "4.18.0",
			},
		}

		ps, err := NewProviderSet().
			WithReleaseProvider(fakeProvider).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		result, err := ps.Lookup(t.Context(), "quay.io/ocp:4.17", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.ImageStream.Name).To(Equal("4.17.5"))

		result, err = ps.Lookup(t.Context(), "quay.io/ocp:4.18", []byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.ImageStream.Name).To(Equal("4.18.0"))
	})

	t.Run("When the injected releaseinfo.Provider returns an error, it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)

		// FakeReleaseProvider with ImageVersion set but missing the requested image
		// causes Lookup to return an error.
		fakeProvider := &fakereleaseprovider.FakeReleaseProvider{
			ImageVersion: map[string]string{
				"quay.io/ocp:4.17": "4.17.0",
			},
		}

		ps, err := NewProviderSet().
			WithReleaseProvider(fakeProvider).
			Build()
		g.Expect(err).ToNot(HaveOccurred())

		result, err := ps.Lookup(t.Context(), "quay.io/ocp:nonexistent", []byte("secret"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unable to lookup release image"))
		g.Expect(result).To(BeNil())
	})
}
