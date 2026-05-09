package imageresolution

import (
	"context"

	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	hyperutil "github.com/openshift/hypershift/support/util"

	"github.com/docker/distribution"
	digest "github.com/opencontainers/go-digest"
)

type imageMetadataProviderAdapter struct {
	ps          *ProviderSet
	rawDelegate *hyperutil.RegistryClientImageMetadataProvider
}

// ImageMetadataProvider returns an ImageMetadataProvider backed by this ProviderSet.
// ImageMetadata calls go through the internal resolver; GetManifest, GetDigest,
// GetMetadata, and GetOverride delegate to the raw registry client with ICSP/IDMS
// mirrors applied but without CLI override resolution.
func (ps *ProviderSet) ImageMetadataProvider() hyperutil.ImageMetadataProvider {
	if ps.testMetadataProvider != nil {
		return ps.testMetadataProvider
	}
	return &imageMetadataProviderAdapter{
		ps:          ps,
		rawDelegate: ps.rawMetadata,
	}
}

func (a *imageMetadataProviderAdapter) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return a.ps.metadata.ImageMetadata(ctx, imageRef, pullSecret)
}

func (a *imageMetadataProviderAdapter) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	a.ps.rawMetadataMu.RLock()
	defer a.ps.rawMetadataMu.RUnlock()
	return a.rawDelegate.GetManifest(ctx, imageRef, pullSecret)
}

func (a *imageMetadataProviderAdapter) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	a.ps.rawMetadataMu.RLock()
	defer a.ps.rawMetadataMu.RUnlock()
	return a.rawDelegate.GetDigest(ctx, imageRef, pullSecret)
}

func (a *imageMetadataProviderAdapter) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	a.ps.rawMetadataMu.RLock()
	defer a.ps.rawMetadataMu.RUnlock()
	return a.rawDelegate.GetMetadata(ctx, imageRef, pullSecret)
}

func (a *imageMetadataProviderAdapter) GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error) {
	a.ps.rawMetadataMu.RLock()
	defer a.ps.rawMetadataMu.RUnlock()
	return a.rawDelegate.GetOverride(ctx, imageRef, pullSecret)
}
