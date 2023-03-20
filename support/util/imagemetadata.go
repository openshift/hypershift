package util

import (
	"context"
	"fmt"
	"net/http"

	"github.com/docker/distribution/registry/client/transport"
	"github.com/golang/groupcache/lru"
	"k8s.io/client-go/rest"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest/dockercredentials"
)

var (
	imageMetadataCache = lru.New(1000)
)

type ImageMetadataProvider interface {
	ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)
}

type RegistryClientImageMetadataProvider struct{}

// ImageMetadata returns metadata for a given image using the given pull secret
// to authenticate. This lookup uses a cache based on the image digest. If the
// reference of the image contains a digest (which is the mainline case for images in a release payload),
// the digest is parsed from the image reference and then used to lookup image metadata in the
// cache. When the image reference does not contain a digest, a lookup is made to the registry to
// fetch the digest of the image that the tag refers to. This is because the actual image that the
// tag is referring to could have changed. Once a digest is obtained, the cache is checked so that
// no further fetching occurs. Only if both cache lookups fail, the image metadata is fetched and
// stored in the cache.
func (*RegistryClientImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {

	ref, err := reference.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}

	// If the image reference contains a digest, immediately look it up in the cache
	if ref.ID != "" {
		if imageConfigObject, exists := imageMetadataCache.Get(string(ref.ID)); exists {
			return imageConfigObject.(*dockerv1client.DockerImageConfig), nil
		}
	}

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

	repo, err := registryContext.Repository(ctx, ref.DockerClientDefaults().RegistryURL(), ref.RepositoryName(), false)
	if err != nil {
		return nil, fmt.Errorf("failed to create repository client for %s: %w", ref.DockerClientDefaults().RegistryURL(), err)
	}
	firstManifest, location, err := manifest.FirstManifest(ctx, ref, repo)
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

// ImageLabels returns labels on a given image metadata
func ImageLabels(metadata *dockerv1client.DockerImageConfig) map[string]string {
	if metadata.Config != nil {
		return metadata.Config.Labels
	} else {
		return metadata.ContainerConfig.Labels
	}
}
