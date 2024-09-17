package util

import (
	"context"
	"fmt"
	"net/http"

	"github.com/docker/distribution"

	"github.com/blang/semver"
	"github.com/docker/distribution/registry/client/transport"
	"github.com/golang/groupcache/lru"
	"k8s.io/client-go/rest"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest/dockercredentials"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	imageMetadataCache = lru.New(1000)
)

type ImageMetadataProvider interface {
	ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)
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
	log := ctrl.LoggerFrom(ctx)

	var (
		ref            *reference.DockerImageReference
		parsedImageRef reference.DockerImageReference
		err            error
		overrideFound  bool
	)

	parsedImageRef, err = reference.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	// There are no ICSPs/IDMSs to process.
	// That means the image reference should be pulled from the external registry
	if len(r.OpenShiftImageRegistryOverrides) == 0 {
		parsedImageRef, err = reference.Parse(imageRef)
		if err != nil {
			return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
		}

		// If the image reference contains a digest, immediately look it up in the cache
		if parsedImageRef.ID != "" {
			if imageConfigObject, exists := imageMetadataCache.Get(parsedImageRef.ID); exists {
				return imageConfigObject.(*dockerv1client.DockerImageConfig), nil
			}
		}

		ref = &parsedImageRef
		_, err = getRepository(ctx, *ref, pullSecret)
		if err != nil {
			return nil, err
		}
	}

	// Get the image repo info based the source/mirrors in the ICSPs/IDMSs
	for source, mirrors := range r.OpenShiftImageRegistryOverrides {
		for _, mirror := range mirrors {
			ref, overrideFound, err = GetRegistryOverrides(ctx, parsedImageRef, source, mirror)
			if err != nil {
				log.Info(fmt.Sprintf("failed to find registry override for image reference %q with source, %s, mirror %s: %s", imageRef, source, mirror, err.Error()))
				continue
			}
			break
		}
		// We found a successful source/mirror combo so break continuing any further source/mirror combos
		if overrideFound {
			break
		}
	}

	// If the image reference contains a digest, immediately look it up in the cache
	if ref.ID != "" {
		if imageConfigObject, exists := imageMetadataCache.Get(ref.ID); exists {
			return imageConfigObject.(*dockerv1client.DockerImageConfig), nil
		}
	}

	repo, err := getRepository(ctx, *ref, pullSecret)
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

func HCControlPlaneReleaseImage(hcluster *hyperv1.HostedCluster) string {
	if hcluster.Spec.ControlPlaneRelease != nil {
		return hcluster.Spec.ControlPlaneRelease.Image
	}
	return hcluster.Spec.Release.Image
}

func GetRegistryOverrides(ctx context.Context, ref reference.DockerImageReference, source string, mirror string) (*reference.DockerImageReference, bool, error) {
	log := ctrl.LoggerFrom(ctx)

	sourceRef, err := reference.Parse(source)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse source image reference %q: %w", source, err)
	}

	if sourceRef.Namespace == ref.Namespace && sourceRef.Name == ref.Name {
		log.Info("registry override coincidence found", "original", fmt.Sprintf("%s/%s/%s", ref.Registry, ref.Namespace, ref.Name), "mirror", mirror)
		mirrorRef, err := reference.Parse(mirror)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse mirror image reference %q: %w", mirrorRef.Name, err)
		}
		return &mirrorRef, true, nil
	}

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
