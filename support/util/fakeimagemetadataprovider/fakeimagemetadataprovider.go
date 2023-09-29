package fakeimagemetadataprovider

import (
	"context"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
)

type FakeImageMetadataProvider struct {
	Result *dockerv1client.DockerImageConfig
}

func (f *FakeImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return f.Result, nil
}
