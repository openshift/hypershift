package fakeimagemetadataprovider

import (
	"context"
	"fmt"

	"github.com/docker/distribution"
	"github.com/opencontainers/go-digest"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	"github.com/openshift/api/image/docker10"

	"github.com/blang/semver"
)

type FakeImageMetadataProvider interface {
	ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)
	GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error)
	GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error)
}

func (f *FakeRegistryClientImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return f.Result, nil
}

func (f *FakeManifest) References() []distribution.Descriptor { return []distribution.Descriptor{} }
func (f *FakeManifest) Payload() (string, []byte, error)      { return f.MediaType, []byte{}, nil }

type FakeRegistryClientImageMetadataProvider struct {
	MediaType string
	Result    *dockerv1client.DockerImageConfig
	Manifest  FakeManifest
	Digest    string
	Ref       *reference.DockerImageReference
}
type FakeManifest struct {
	MediaType string
}

func (f *FakeRegistryClientImageMetadataProvider) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	_, _, err := registryclient.GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve manifest %s: %w", imageRef, err)
	}
	return &FakeManifest{
		f.MediaType,
	}, nil
}

func (f *FakeRegistryClientImageMetadataProvider) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	var err error
	_, f.Ref, err = registryclient.GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return "", nil, fmt.Errorf("failed to retrieve manifest %s: %w", imageRef, err)
	}
	f.Ref.ID = f.Digest
	return digest.Digest(f.Digest), f.Ref, nil
}

func (f *FakeRegistryClientImageMetadataProvider) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	return f.Result, []distribution.Descriptor{}, nil, nil
}

func (f *FakeRegistryClientImageMetadataProvider) GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error) {
	return f.Ref, nil
}

type FakeRegistryClientImageMetadataProviderHCCO struct {
}

func (f *FakeRegistryClientImageMetadataProviderHCCO) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	dockerImageRef := &reference.DockerImageReference{
		Registry:  "registry.redhat.io",
		Namespace: "redhat",
	}
	return "", dockerImageRef, nil
}

func (f *FakeRegistryClientImageMetadataProviderHCCO) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return &dockerv1client.DockerImageConfig{}, nil
}

func (f *FakeRegistryClientImageMetadataProviderHCCO) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	return &FakeManifest{}, nil
}

func (f *FakeRegistryClientImageMetadataProviderHCCO) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	imageConfig := &dockerv1client.DockerImageConfig{
		Config: &docker10.DockerConfig{
			Labels: map[string]string{
				"io.openshift.release": semver.MustParse("4.18.0").String(),
			},
		},
	}

	return imageConfig, []distribution.Descriptor{}, nil, nil
}

func (f *FakeRegistryClientImageMetadataProviderHCCO) GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error) {
	return &reference.DockerImageReference{}, nil
}
