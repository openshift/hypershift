package util

import (
	"context"
	"fmt"
	"net/http"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest/dockercredentials"

	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/blang/semver"
	"github.com/docker/distribution"
	"github.com/docker/distribution/registry/client/transport"
	"github.com/golang/groupcache/lru"
	"github.com/opencontainers/go-digest"
)

var (
	imageMetadataCache = lru.New(1000)
	manifestsCache     = lru.New(1000)
	digestCache        = lru.New(1000)
)

type ImageMetadataProvider interface {
	ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)
	GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error)
	GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error)
	GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error)
	GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error)
}

type RegistryClientImageMetadataProvider struct {
	OpenShiftImageRegistryOverrides map[string][]string
}

// ImageMetadata returns metadata for a given image using the given pull secret
// to authenticate. This lookup uses a cache based on the image digest. If the
// reference of the image contains a digest (which is the mainline case for images in a release payload),
// the digest is parsed from the image reference and then used to lookup image metadata in the
// cache. When the image reference does not contain a digest, a lookup is made to the registry to
// fetch the digest of the image that the tag refers to. This is because the actual image that the
// tag is referring to could have changed. Once a digest is obtained, the cache is checked so that
// no further fetching occurs. Only if both cache lookups fail, the image metadata is fetched and
// stored in the cache.
func (r *RegistryClientImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {

	var (
		repo           distribution.Repository
		ref            *reference.DockerImageReference
		parsedImageRef reference.DockerImageReference
		err            error
	)

	parsedImageRef, err = reference.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	// There are no ICSPs/IDMSs to process.
	// That means the image reference should be pulled from the external registry
	if len(r.OpenShiftImageRegistryOverrides) == 0 {
		// If the image reference contains a digest, immediately look it up in the cache
		if parsedImageRef.ID != "" {
			if imageConfigObject, exists := imageMetadataCache.Get(parsedImageRef.ID); exists {
				return imageConfigObject.(*dockerv1client.DockerImageConfig), nil
			}
		}
		ref = &parsedImageRef
	}

	// Get the image repo info based the source/mirrors in the ICSPs/IDMSs
	ref = seekOverride(ctx, r.OpenShiftImageRegistryOverrides, parsedImageRef, pullSecret)

	// If the image reference contains a digest, immediately look it up in the cache
	if ref.ID != "" {
		if imageConfigObject, exists := imageMetadataCache.Get(ref.ID); exists {
			return imageConfigObject.(*dockerv1client.DockerImageConfig), nil
		}
	}

	repo, err = getRepository(ctx, *ref, pullSecret)
	if err != nil || repo == nil {
		return nil, fmt.Errorf("failed to create repository client for %s: %w", ref.DockerClientDefaults().RegistryURL(), err)
	}

	ref.ID = parsedImageRef.ID
	firstManifest, location, err := manifest.FirstManifest(ctx, *ref, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain root manifest for %s: %w", imageRef, err)
	}

	// If the image ref did not contain a digest, attempt looking it up by digest after we've fetched the digest
	if ref.ID == "" {
		if imageConfigObject, exists := imageMetadataCache.Get(string(location.Manifest)); exists {
			return imageConfigObject.(*dockerv1client.DockerImageConfig), nil
		}
	}

	config, _, err := manifest.ManifestToImageConfig(ctx, firstManifest, repo.Blobs(ctx), location)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain image configuration for %s: %w", imageRef, err)
	}
	imageMetadataCache.Add(string(location.Manifest), config)

	return config, nil
}

// GetOverride returns the image reference override based on the source/mirrors in the ICSPs/IDMSs
func (r *RegistryClientImageMetadataProvider) GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error) {

	var (
		ref            *reference.DockerImageReference
		parsedImageRef reference.DockerImageReference
		err            error
	)

	parsedImageRef, err = reference.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	ref = seekOverride(ctx, r.OpenShiftImageRegistryOverrides, parsedImageRef, pullSecret)

	return ref, nil
}

func (r *RegistryClientImageMetadataProvider) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {

	var (
		repo           distribution.Repository
		ref            *reference.DockerImageReference
		parsedImageRef reference.DockerImageReference
		err            error
		srcDigest      digest.Digest
	)

	parsedImageRef, err = reference.Parse(imageRef)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	// There are no ICSPs/IDMSs to process.
	// That means the image reference should be pulled from the external registry
	if len(r.OpenShiftImageRegistryOverrides) == 0 {
		// If the image name is in the cache, return early
		if imageDigest, exists := digestCache.Get(imageRef); exists {
			parsedImageRef.ID = string(imageDigest.(digest.Digest))
			return imageDigest.(digest.Digest), &parsedImageRef, nil
		}

		ref = &parsedImageRef
	}

	// Get the image repo info based the source/mirrors in the ICSPs/IDMSs
	ref = seekOverride(ctx, r.OpenShiftImageRegistryOverrides, parsedImageRef, pullSecret)
	composedRef := ref.String()

	// If the overridden image name is in the cache, return early
	if imageDigest, exists := digestCache.Get(composedRef); exists {
		ref.ID = string(imageDigest.(digest.Digest))
		return imageDigest.(digest.Digest), ref, nil
	}

	repo, composedParsedRef, err := GetRepoSetup(ctx, composedRef, pullSecret)
	if err != nil || repo == nil {
		return "", nil, fmt.Errorf("failed to create repository client for %s: %w", ref.DockerClientDefaults().RegistryURL(), err)
	}

	switch {
	case len(composedParsedRef.ID) > 0:
		srcDigest = digest.Digest(composedParsedRef.ID)

	case len(composedParsedRef.Tag) > 0:
		desc, err := repo.Tags(ctx).Get(ctx, composedParsedRef.Tag)
		if err != nil {
			fmt.Printf("failed to get repository tags for %s composedParsedRef: %+v: %v. Falling back to the original imageRef %s.\n", composedParsedRef.Tag, composedParsedRef, err, imageRef)
			if desc, err = fallbackToOriginalImageRef(ctx, imageRef, pullSecret); err != nil {
				return "", nil, fmt.Errorf("failed to fallback to original imageRef %s: %w", imageRef, err)
			}
		}
		srcDigest = desc.Digest
		composedParsedRef.ID = string(srcDigest)
	}

	digestCache.Add(composedRef, srcDigest)
	digestCache.Add(imageRef, srcDigest)

	return srcDigest, composedParsedRef, nil
}

// GetManifest returns the manifest for a given image using the given pull secret
// to authenticate. This lookup uses a cache based on the image digest. If The
// reference of the image contains a digest (which is the mainline case for images in a release payload),
// the digest is parsed from the image reference and then used to lookup the manifest in the
// cache and return it with the ImageOverrides already included.
func (r *RegistryClientImageMetadataProvider) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {

	var (
		ref            *reference.DockerImageReference
		parsedImageRef reference.DockerImageReference
		err            error
		srcDigest      digest.Digest
	)

	parsedImageRef, err = reference.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	// There are no ICSPs/IDMSs to process.
	// That means the image reference should be pulled from the external registry
	if len(r.OpenShiftImageRegistryOverrides) == 0 {
		// If the image reference contains a digest, immediately look it up in the cache
		if parsedImageRef.ID != "" {
			if manifest, exists := manifestsCache.Get(parsedImageRef.ID); exists {
				return manifest.(distribution.Manifest), nil
			}
		}
		ref = &parsedImageRef
	}

	// Get the image repo info based the source/mirrors in the ICSPs/IDMSs
	ref = seekOverride(ctx, r.OpenShiftImageRegistryOverrides, parsedImageRef, pullSecret)

	// If the image reference contains a digest, immediately look it up in the cache
	if ref.ID != "" {
		if manifest, exists := manifestsCache.Get(ref.ID); exists {
			return manifest.(distribution.Manifest), nil
		}
	}

	composedRef := ref.String()

	digestsManifest, srcDigest, err := getManifest(ctx, composedRef, pullSecret)
	if err != nil {
		return nil, err
	}
	manifestsCache.Add(srcDigest, digestsManifest)

	return digestsManifest, nil
}

func (r *RegistryClientImageMetadataProvider) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {

	var (
		ref            *reference.DockerImageReference
		parsedImageRef reference.DockerImageReference
		err            error
	)

	if len(r.OpenShiftImageRegistryOverrides) == 0 {
		return getMetadata(ctx, imageRef, pullSecret)
	}

	parsedImageRef, err = reference.Parse(imageRef)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	// Get the image repo info based the source/mirrors in the ICSPs/IDMSs
	ref = seekOverride(ctx, r.OpenShiftImageRegistryOverrides, parsedImageRef, pullSecret)
	composedRef := ref.String()

	return getMetadata(ctx, composedRef, pullSecret)
}

// getManifest gets the manifest from an image
func getManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, digest.Digest, error) {
	repo, ref, err := GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, "", err
	}

	var srcDigest digest.Digest
	if len(ref.Tag) > 0 {
		desc, err := repo.Tags(ctx).Get(ctx, ref.Tag)
		if err != nil {
			return nil, "", err
		}
		srcDigest = desc.Digest
	}

	if len(ref.ID) > 0 {
		srcDigest = digest.Digest(ref.ID)
	}

	manifests, err := repo.Manifests(ctx)
	if err != nil {
		return nil, "", err
	}

	digestsManifest, err := manifests.Get(ctx, srcDigest, manifest.PreferManifestList)
	if err != nil {
		return nil, "", err
	}

	return digestsManifest, srcDigest, nil
}

func getMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	repo, ref, err := GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get repo setup: %w", err)
	}
	firstManifest, location, err := manifest.FirstManifest(ctx, *ref, repo)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to obtain root manifest for %s: %w", imageRef, err)
	}
	imageConfig, layers, err := manifest.ManifestToImageConfig(ctx, firstManifest, repo.Blobs(ctx), location)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to obtain image layers for %s: %w", imageRef, err)
	}
	return imageConfig, layers, repo.Blobs(ctx), nil
}

// GetRepoSetup connects to a repo and pulls the imageRef's docker image information from the repo. Returns the repo and the docker image.
func GetRepoSetup(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error) {
	var dockerImageRef *reference.DockerImageReference
	rt, err := rest.TransportFor(&rest.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create secure transport: %w", err)
	}
	insecureRT, err := rest.TransportFor(&rest.Config{TLSClientConfig: rest.TLSClientConfig{Insecure: true}})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create insecure transport: %w", err)
	}
	credStore, err := dockercredentials.NewFromBytes(pullSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("GetRepoSetup - failed to parse docker credentials: %w", err)
	}
	registryContext := registryclient.NewContext(rt, insecureRT).WithCredentials(credStore).
		WithRequestModifiers(transport.NewHeaderRequestModifier(http.Header{http.CanonicalHeaderKey("User-Agent"): []string{rest.DefaultKubernetesUserAgent()}}))

	ref, err := reference.Parse(imageRef)
	dockerImageRef = &ref
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}
	repo, err := registryContext.Repository(ctx, ref.DockerClientDefaults().RegistryURL(), ref.RepositoryName(), false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create repository client for %s: %w", ref.DockerClientDefaults().RegistryURL(), err)
	}
	return repo, dockerImageRef, nil
}

func getRepository(ctx context.Context, ref reference.DockerImageReference, pullSecret []byte) (distribution.Repository, error) {
	credStore, err := dockercredentials.NewFromBytes(pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to parse docker credentials: %w", err)
	}
	rt, err := rest.TransportFor(&rest.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create secure transport: %w", err)
	}
	registryContext := registryclient.NewContext(rt, nil).WithCredentials(credStore).
		WithRequestModifiers(transport.NewHeaderRequestModifier(http.Header{http.CanonicalHeaderKey("User-Agent"): []string{rest.DefaultKubernetesUserAgent()}}))

	return registryContext.Repository(ctx, ref.DockerClientDefaults().RegistryURL(), ref.RepositoryName(), false)
}

// ImageLabels returns labels on a given image metadata
func ImageLabels(metadata *dockerv1client.DockerImageConfig) map[string]string {
	if metadata.Config != nil {
		return metadata.Config.Labels
	} else {
		return metadata.ContainerConfig.Labels
	}
}

// Attempts only a root registry override match.
func tryOnlyRootRegistryOverride(ref, sourceRef, mirrorRef reference.DockerImageReference) (*reference.DockerImageReference, bool, error) {
	if sourceRef.Namespace == "" && sourceRef.Registry == "" && sourceRef.Name != "" {
		if ref.Registry == sourceRef.Name {
			composedImage := buildComposedRef(mirrorRef.String(), ref.Namespace, ref.NameString())
			composedRef, err := reference.Parse(composedImage)
			if err != nil {
				return nil, false, fmt.Errorf("failed to parse composed image reference (root registry match): %w", err)
			}
			return &composedRef, true, nil
		}
	}
	return nil, false, nil
}

// Attempts only a namespace override match.
func tryOnlyNamespaceOverride(ref, sourceRef, mirrorRef reference.DockerImageReference) (*reference.DockerImageReference, bool, error) {
	if sourceRef.Namespace == "" {
		if sourceRef.Name == ref.Namespace {
			composedImage := buildComposedRef(mirrorRef.Registry, mirrorRef.Name, ref.NameString())
			composedRef, err := reference.Parse(composedImage)
			if err != nil {
				return nil, false, fmt.Errorf("failed to parse composed image reference (namespace match): %w", err)
			}
			return &composedRef, true, nil
		}
	}
	return nil, false, nil
}

// Attempts only an exact repository override match.
func tryExactCoincidenceOverride(ref, sourceRef, mirrorRef reference.DockerImageReference) (*reference.DockerImageReference, bool, error) {
	if ref.Namespace == sourceRef.Namespace && ref.Name == sourceRef.Name {
		mirrorRef.ID = ref.ID
		mirrorRef.Tag = ref.Tag
		composedImage := buildComposedRef(mirrorRef.Registry, mirrorRef.Namespace, mirrorRef.NameString())
		composedRef, err := reference.Parse(composedImage)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse composed image reference (exact match): %w", err)
		}
		return &composedRef, true, nil
	}
	return nil, false, nil
}

func GetRegistryOverrides(ctx context.Context, ref reference.DockerImageReference, source, mirror string) (*reference.DockerImageReference, bool, error) {
	log := ctrl.LoggerFrom(ctx)

	sourceRef, err := reference.Parse(source)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse source image reference %q: %w", source, err)
	}

	mirrorRef, err := reference.Parse(mirror)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse mirror image reference %q: %w", mirror, err)
	}

	// Try only root registry override
	if composedRef, found, err := tryOnlyRootRegistryOverride(ref, sourceRef, mirrorRef); found || err != nil {
		if found {
			log.Info("registry override found (root registry match)", "original", buildComposedRef(sourceRef.Name, ref.Namespace, ref.NameString()), "mirror", mirror, "composed", composedRef)
		}
		return composedRef, found, err
	}

	// Try only namespace override
	if composedRef, found, err := tryOnlyNamespaceOverride(ref, sourceRef, mirrorRef); found || err != nil {
		if found {
			log.Info("registry override found (namespace match)", "original", buildComposedRef(ref.Registry, ref.Namespace, ref.NameString()), "mirror", mirror, "composed", composedRef)
		}
		return composedRef, found, err
	}

	// Try only exact repository override
	if composedRef, found, err := tryExactCoincidenceOverride(ref, sourceRef, mirrorRef); found || err != nil {
		if found {
			log.Info("registry override found (exact match)", "original", buildComposedRef(ref.Registry, ref.Namespace, ref.NameString()), "mirror", mirror, "composed", composedRef)
		}
		return composedRef, found, err
	}

	// No match found
	return &ref, false, nil
}

func GetPayloadImage(ctx context.Context, releaseImageProvider releaseinfo.Provider, hc *hyperv1.HostedCluster, component string, pullSecret []byte) (string, error) {
	releaseImage, err := releaseImageProvider.Lookup(ctx, HCControlPlaneReleaseImage(hc), pullSecret)
	if err != nil {
		return "", fmt.Errorf("failed to lookup release image: %w", err)
	}

	image, exists := releaseImage.ComponentImages()[component]
	if !exists {
		return "", fmt.Errorf("image does not exist for release: %q", image)
	}
	return image, nil
}

func GetPayloadVersion(ctx context.Context, releaseImageProvider releaseinfo.Provider, hc *hyperv1.HostedCluster, pullSecret []byte) (*semver.Version, error) {
	releaseImage, err := releaseImageProvider.Lookup(ctx, HCControlPlaneReleaseImage(hc), pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup release image: %w", err)
	}
	versionStr := releaseImage.Version()
	version, err := semver.Parse(versionStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse version (%s): %w", versionStr, err)
	}
	return &version, nil
}

func seekOverride(ctx context.Context, openshiftImageRegistryOverrides map[string][]string, parsedImageReference reference.DockerImageReference, pullSecret []byte) *reference.DockerImageReference {
	log := ctrl.LoggerFrom(ctx)
	for source, mirrors := range openshiftImageRegistryOverrides {
		for _, mirror := range mirrors {
			ref, overrideFound, err := GetRegistryOverrides(ctx, parsedImageReference, source, mirror)
			if err != nil {
				log.Info(fmt.Sprintf("failed to find registry override for image reference %q with source, %s, mirror %s: %s", parsedImageReference, source, mirror, err.Error()))
				continue
			}
			if overrideFound {
				// Verify mirror image availability.
				if _, _, _, err = getMetadata(ctx, ref.String(), pullSecret); err == nil {
					return ref
				}
				log.Info("WARNING: The current mirrors image is unavailable, continue Scanning multiple mirrors", "error", err.Error(), "mirror image", ref)
				continue
			}
		}
	}
	return &parsedImageReference
}

// buildComposedRef creates a docker image pull reference given its
// separate components
func buildComposedRef(registry, namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s", registry, namespace, name)
}

// fallbackToOriginalImageRef tries to get the repository tags for the original imageRef not having in mind the overrides.
func fallbackToOriginalImageRef(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Descriptor, error) {
	repo, ref, err := GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return distribution.Descriptor{}, fmt.Errorf("failed on fallback getting the repo setup for %s: %w", imageRef, err)
	}

	desc, err := repo.Tags(ctx).Get(ctx, ref.Tag)
	if err != nil {
		return distribution.Descriptor{}, fmt.Errorf("failed on fallback getting the repository tags for %s: %w", ref.Tag, err)
	}

	return desc, nil
}
